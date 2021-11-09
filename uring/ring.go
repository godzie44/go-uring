package uring

import (
	"errors"
	"fmt"
	"runtime"
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

type Ring struct {
	fd int

	Params *ringParams

	cqRing *cq
	sqRing *sq
}

var ErrRingSetup = errors.New("ring setup")

type SetupOption func(params *ringParams)

func WithCQSize(sz uint32) SetupOption {
	return func(params *ringParams) {
		params.flags = params.flags | setupCQSize
		params.cqEntries = sz
	}
}

func WithIOPoll() SetupOption {
	return func(params *ringParams) {
		params.flags = params.flags | setupIOPoll
	}
}

func WithIOWQMaxWorkers(count int) SetupOption {
	return func(params *ringParams) {
		params.flags = params.flags | setupIOPoll
	}
}

func New(entries uint32, opts ...SetupOption) (*Ring, error) {
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

	r := &Ring{Params: &params, fd: fd, sqRing: &sq{}, cqRing: &cq{}}
	err = r.allocRing(&params)

	return r, err
}

type Defer func() error

func CreateMany(count int, entries uint32, opts ...SetupOption) ([]*Ring, Defer, error) {
	var defers = map[int]func() error{}
	var rings []*Ring
	for i := 0; i < count; i++ {
		r, err := New(entries, opts...)
		if err != nil {
			for _, closeFn := range defers {
				closeFn()
			}
			return nil, nil, err
		}
		defers[r.fd] = r.Close
		rings = append(rings, r)
	}

	return rings, func() (err error) {
		for fd, closeFn := range defers {
			cErr := closeFn()
			if cErr != nil {
				err = fmt.Errorf("%w, close ring: %d error: %s", err, fd, cErr.Error())
			}
		}
		return err
	}, nil
}

func (r *Ring) Fd() int {
	return r.fd
}

func (r *Ring) Close() error {
	err := r.freeRing()
	return joinErr(err, syscall.Close(r.fd))
}

var ErrSQRingOverflow = errors.New("sq ring overflow")

func (r *Ring) NextSQE() (entry *SQEntry, err error) {
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

type Operation interface {
	PrepSQE(*SQEntry)
	Code() OpCode
}

func (r *Ring) QueueSQE(op Operation, flags uint8, userData uint64) error {
	sqe, err := r.NextSQE()
	if err != nil {
		return err
	}

	op.PrepSQE(sqe)
	sqe.Flags = flags
	sqe.setUserData(userData)
	return nil
}

func (r *Ring) Submit() (uint, error) {
	flushed := r.flushSQ()

	var flags uint32
	if r.Params.flags&setupIOPoll == 1 {
		flags |= sysRingEnterGetEvents
	}

	consumed, err := sysEnter(r.fd, flushed, 0, flags, nil)
	return consumed, err
}

var _sizeOfUint32 = unsafe.Sizeof(uint32(0))

func (r *Ring) flushSQ() uint32 {
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

type getParams struct {
	submit, waitNr uint32
	flags          uint32
	arg            unsafe.Pointer
	sz             int
}

func (r *Ring) getCQEvents(params getParams) (cqe *CQEvent, err error) {
	for {
		var needEnter = false
		var cqOverflowFlush = false
		var flags uint32
		var available uint32

		available, cqe, err = r.peekCQEvent()
		if err != nil {
			break
		}

		if cqe == nil && params.waitNr == 0 && params.submit == 0 {
			if !r.sqRing.cqNeedFlush() {
				err = syscall.EAGAIN
				break
			}
			cqOverflowFlush = true
		}

		if params.waitNr > available || cqOverflowFlush {
			flags = sysRingEnterGetEvents | params.flags
			needEnter = true
		}

		if params.submit != 0 {
			needEnter = true
		}

		if !needEnter {
			break
		}

		var consumed uint
		consumed, err = sysEnter2(r.fd, params.submit, params.waitNr, flags, params.arg, params.sz)

		if err != nil {
			break
		}
		params.submit -= uint32(consumed)
		if cqe != nil {
			break
		}
	}

	return cqe, err
}

func (r *Ring) WaitCQEventsWithTimeout(count uint32, timeout time.Duration) (cqe *CQEvent, err error) {
	if r.Params.ExtArgFeature() {
		ts := syscall.NsecToTimespec(timeout.Nanoseconds())
		arg := newGetEventsArg(uintptr(unsafe.Pointer(nil)), numSig/8, uintptr(unsafe.Pointer(&ts)))

		cqe, err = r.getCQEvents(getParams{
			submit: 0,
			waitNr: count,
			flags:  sysRingEnterExtArg,
			arg:    unsafe.Pointer(arg),
			sz:     int(unsafe.Sizeof(getEventsArg{})),
		})

		runtime.KeepAlive(arg)
		runtime.KeepAlive(ts)
		return cqe, err
	}

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

	op := Timeout(timeout)
	op.PrepSQE(sqe)
	sqe.setUserData(libUserDataTimeout)
	toSubmit = r.flushSQ()

	return r.getCQEvents(getParams{
		submit: toSubmit,
		waitNr: count,
		arg:    unsafe.Pointer(nil),
		sz:     numSig / 8,
	})
}

func (r *Ring) WaitCQEvents(count uint32) (cqe *CQEvent, err error) {
	return r.getCQEvents(getParams{
		submit: 0,
		waitNr: count,
		arg:    unsafe.Pointer(nil),
		sz:     numSig / 8,
	})
}

func (r *Ring) SubmitAndWaitCQEvents(count uint32) (cqe *CQEvent, err error) {
	return r.getCQEvents(getParams{
		submit: r.flushSQ(),
		waitNr: count,
		arg:    unsafe.Pointer(nil),
		sz:     numSig / 8,
	})
}

func (r *Ring) PeekCQE() (*CQEvent, error) {
	return r.WaitCQEvents(0)
}

func (r *Ring) SeenCQE(cqe *CQEvent) {
	r.AdvanceCQ(1)
}

func (r *Ring) AdvanceCQ(n uint32) {
	atomic.AddUint32(r.cqRing.kHead, n)
}

func (r *Ring) peekCQEvent() (uint32, *CQEvent, error) {
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

		if !r.Params.ExtArgFeature() && cqe.UserData == libUserDataTimeout {
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

func (r *Ring) peekCQEventBatch(buff []*CQEvent) int {
	ready := r.cqRing.readyCount()
	count := min(uint32(len(buff)), ready)

	if ready != 0 {
		head := atomic.LoadUint32(r.cqRing.kHead)
		mask := atomic.LoadUint32(r.cqRing.kRingMask)

		last := head + count
		for i := 0; head != last; head, i = head+1, i+1 {
			buff[i] = (*CQEvent)(unsafe.Add(unsafe.Pointer(r.cqRing.cqeBuff), uintptr(head&mask)*unsafe.Sizeof(CQEvent{})))
		}
	}
	return int(count)
}

func min(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

func (r *Ring) PeekCQEventBatch(buff []*CQEvent) int {
	n := r.peekCQEventBatch(buff)
	if n == 0 {
		if r.sqRing.cqNeedFlush() {
			_, _ = sysEnter(r.fd, 0, 0, sysRingEnterGetEvents, nil)
			n = r.peekCQEventBatch(buff)
		}
	}

	return n
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
