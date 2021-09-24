package uring

import (
	"errors"
	"fmt"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

type sq struct {
	buff         []byte
	sqeBuff      []byte
	ringSize     uint64
	kHead        *uint32
	kTail        *uint32
	kRingMask    *uint32
	kRingEntries *uint32
	kFlags       *uint32
	kDropped     *uint32
	kArray       *uint32

	sqeTail, sqeHead uint32
}

func (s *sq) cqNeedFlush() bool {
	return atomic.LoadUint32(s.kFlags)&sqCQOverflow != 0
}

type cq struct {
	buff         []byte
	ringSize     uint64
	kHead        *uint32
	kTail        *uint32
	kRingMask    *uint32
	kRingEntries *uint32
	kOverflow    *uint32
	kFlags       uintptr
	cqeBuff      *CQEvent
}

func (c *cq) readyCount() uint32 {
	return atomic.LoadUint32(c.kTail) - atomic.LoadUint32(c.kHead)
}

const MaxEntries uint32 = 1 << 15

type URing struct {
	fd int

	params *ringParams

	cqRing *cq
	sqRing *sq

	result chan string
}

var ErrRingSetup = errors.New("ring setup")

type URingOption func(params *ringParams)

func WithCQSize(sz uint32) URingOption {
	return func(params *ringParams) {
		params.flags = params.flags | setupCQSize
		params.cqEntries = sz
	}
}

func NewRing(entries uint32, opts ...URingOption) (*URing, error) {
	if entries > MaxEntries {
		return nil, ErrRingSetup
	}

	params := ringParams{}

	for _, opt := range opts {
		opt(&params)
	}

	fd, err := sysSetup(entries, &params)
	if err != nil {
		return nil, err
	}

	r := &URing{params: &params, fd: fd, sqRing: &sq{}, cqRing: &cq{}, result: make(chan string)}
	err = r.allocRing(&params)

	return r, err
}

func (r *URing) Close() error {
	close(r.result)
	err := r.freeRing()
	return joinErr(err, syscall.Close(r.fd))
}

var ErrSQRingOverflow = errors.New("sq ring overflow")

func (r *URing) NextSQE() (entry *SQEntry, err error) {
	head := atomic.LoadUint32(r.sqRing.kHead)
	next := r.sqRing.sqeTail + 1

	if next-head <= *r.sqRing.kRingEntries {
		idx := r.sqRing.sqeTail & *r.sqRing.kRingMask * uint32(unsafe.Sizeof(SQEntry{}))
		entry = (*SQEntry)(unsafe.Pointer(&r.sqRing.sqeBuff[idx]))
		r.sqRing.sqeTail = next
	} else {
		err = ErrSQRingOverflow
	}

	return entry, err
}

func (r *URing) FillNextSQE(filler func(sqe *SQEntry)) error {
	sqe, err := r.NextSQE()
	if err != nil {
		return err
	}
	filler(sqe)
	return nil
}

func (r *URing) Submit() (uint, error) {
	flushed := r.flushSQ()

	consumed, err := sysEnter(r.fd, flushed, 0, 0, nil)
	return consumed, err
}

var _sizeOfUint32 = unsafe.Sizeof(uint32(0))

func (r *URing) flushSQ() uint32 {
	mask := *r.sqRing.kRingMask
	tail := atomic.LoadUint32(r.sqRing.kTail)
	subCnt := r.sqRing.sqeTail - r.sqRing.sqeHead

	if subCnt == 0 {
		return tail - atomic.LoadUint32(r.sqRing.kHead)
	}

	for i := subCnt; i > 0; i-- {
		*(*uint32)(unsafe.Add(unsafe.Pointer(r.sqRing.kArray), tail&mask*uint32(_sizeOfUint32))) = r.sqRing.sqeHead & mask
		tail++
		r.sqRing.sqeHead++
	}

	atomic.StoreUint32(r.sqRing.kTail, tail)

	return tail - atomic.LoadUint32(r.sqRing.kHead)
}

func (r *URing) getCQEvents(submit, count uint32) (cqe *CQEvent, err error) {
	for {
		var needEnter = false
		var cqOverflowFlush = false
		var flags uint32
		var available uint32

		available, cqe, err = r.peekCQEvent()
		if err != nil {
			break
		}

		if cqe == nil && count == 0 && submit == 0 {
			if !r.sqRing.cqNeedFlush() {
				err = syscall.EAGAIN
				break
			}
			cqOverflowFlush = true
		}

		if count > available || cqOverflowFlush {
			flags = sysRingEnterGetEvents
			needEnter = true
		}

		if submit != 0 {
			needEnter = true
		}

		if !needEnter {
			break
		}

		var consumed uint
		consumed, err = sysEnter2(r.fd, submit, count, flags, nil, numSig/8)

		if err != nil {
			break
		}
		submit -= uint32(consumed)
		if cqe != nil {
			break
		}
	}

	return cqe, err
}

func (r *URing) WaitCQEventsWithTimeout(count uint32, timeout time.Duration) (cqe *CQEvent, err error) {
	var toSubmit uint32

	var sqe *SQEntry
	sqe, err = r.NextSQE()
	if err != nil {
		_, err = r.Submit()
		if err != nil {
			return nil, err
		}

		sqe, err = r.NextSQE()
		if err != nil {
			return nil, err
		}
	}

	cmd := Timeout(timeout)
	cmd.SetUserData(libUserDataTimeout)
	cmd.fillSQE(sqe)
	toSubmit = r.flushSQ()

	return r.getCQEvents(toSubmit, count)
}

func (r *URing) WaitCQEvents(count uint32) (cqe *CQEvent, err error) {
	return r.getCQEvents(0, count)
}

func (r *URing) PeekCQE() (*CQEvent, error) {
	return r.WaitCQEvents(0)
}

func (r *URing) SeenCQE(cqe *CQEvent) {
	r.AdvanceCQ(1)
}

func (r *URing) AdvanceCQ(n uint32) {
	atomic.AddUint32(r.cqRing.kHead, n)
}

func (r *URing) peekCQEvent() (uint32, *CQEvent, error) {
	mask := *r.cqRing.kRingMask
	var cqe *CQEvent
	var available uint32

	var err error
	for {
		tail := atomic.LoadUint32(r.cqRing.kTail)
		head := atomic.LoadUint32(r.cqRing.kHead)

		cqe = nil
		available = tail - head
		if available == 0 {
			break
		}

		cqe = (*CQEvent)(unsafe.Add(unsafe.Pointer(r.cqRing.cqeBuff), uintptr(head&mask)*unsafe.Sizeof(CQEvent{})))

		if cqe.UserData == libUserDataTimeout {
			if cqe.Res < 0 {
				err = cqe.Error()
			}
			r.SeenCQE(cqe)
			if err == nil {
				continue
			}
			cqe = nil
		}
		break
	}

	return available, cqe, err
}

func (r *URing) peekCQEventBatch(count uint32) (result []*CQEvent) {
	ready := r.cqRing.readyCount()
	if ready != 0 {
		head := atomic.LoadUint32(r.cqRing.kHead)
		mask := atomic.LoadUint32(r.cqRing.kRingMask)

		if count > ready {
			count = ready
		}

		last := head + count
		result = make([]*CQEvent, 0, last-head)
		for ; head != last; head++ {
			result = append(result, (*CQEvent)(unsafe.Add(unsafe.Pointer(r.cqRing.cqeBuff), uintptr(head&mask)*unsafe.Sizeof(CQEvent{}))))
		}
	}
	return result
}

func (r *URing) PeekCQEventBatch(count uint32) []*CQEvent {
	result := r.peekCQEventBatch(count)
	if result == nil {
		if r.sqRing.cqNeedFlush() {
			_, _ = sysEnter(r.fd, 0, 0, sysRingEnterGetEvents, nil)
			result = r.peekCQEventBatch(count)
		}
	}

	return result
}

type probeOp struct {
	Op    uint8
	_res  uint8
	Flags uint16
	_res2 uint32
}

type Probe struct {
	lastOp uint8
	opsLen uint8
	_res   uint16
	_res2  [3]uint32
	ops    *probeOp
}

func (p *Probe) GetOP(n int) *probeOp {
	return (*probeOp)(unsafe.Add(unsafe.Pointer(p.ops), uintptr(n)*unsafe.Sizeof(probeOp{})))
}

func (r *URing) Probe() (*Probe, error) {
	opts := make([]probeOp, 256)
	//len := unsafe.Sizeof(Probe{}) + 256 * unsafe.Sizeof(probeOp{})
	probe := &Probe{
		ops: &opts[0],
	}

	err := sysRegisterProbe(r.fd, probe, 256)

	return probe, err
}

func joinErr(err1, err2 error) error {
	if err1 == nil {
		return err2
	}
	if err2 == nil {
		return err1
	}

	return fmt.Errorf("multiple errors: %w and %s", err1, err2.Error())
}
