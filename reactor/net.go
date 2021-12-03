//go:build linux

package reactor

import (
	"context"
	"errors"
	"github.com/godzie44/go-uring/uring"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	timeoutNonce = math.MaxUint64
	cancelNonce  = math.MaxUint64 - 1

	cqeBuffSize = 1 << 7
)

//RequestID identifier of operation queued into NetworkReactor.
type RequestID uint64

func packRequestID(fd int, nonce uint32) RequestID {
	return RequestID(uint64(fd) | uint64(nonce)<<32)
}

func (ud RequestID) fd() int {
	var mask = uint64(math.MaxUint32)
	return int(uint64(ud) & mask)
}

func (ud RequestID) nonce() uint32 {
	return uint32(ud >> 32)
}

//NetworkReactor is event loop's manager with main responsibility - handling client requests and return responses asynchronously.
//NetworkReactor optimized for network operations like Accept, Recv, Send.
type NetworkReactor struct {
	loops []*ringNetEventLoop

	registry *registry

	config *configuration
}

//NewNet create NetworkReactor instance.
func NewNet(rings []*uring.Ring, opts ...Option) (*NetworkReactor, error) {
	for _, ring := range rings {
		if err := checkRingReq(ring, true); err != nil {
			return nil, err
		}
	}

	r := &NetworkReactor{
		config: &configuration{
			tickDuration: time.Millisecond * 1,
			logger:       &nopLogger{},
		},
	}

	r.registry = newRegistry(len(rings))

	for _, opt := range opts {
		opt(r.config)
	}

	for _, ring := range rings {
		loop := newRingNetEventLoop(ring, r.config.logger, r.registry)
		r.loops = append(r.loops, loop)
	}

	return r, nil
}

//Run start NetworkReactor.
func (r *NetworkReactor) Run(ctx context.Context) {
	for _, loop := range r.loops {
		go loop.runReader(r.config.tickDuration)
		go loop.runWriter()
	}

	<-ctx.Done()

	for _, loop := range r.loops {
		loop.stopReader()
		loop.stopWriter()
	}
}

//NetOperation must be implemented by NetworkReactor supported operations.
type NetOperation interface {
	uring.Operation
	Fd() int
}

type subSqeRequest struct {
	op       uring.Operation
	flags    uint8
	userData uint64

	timeout time.Duration
}

func (r *NetworkReactor) queue(op NetOperation, cb Callback, timeout time.Duration) RequestID {
	ud := packRequestID(op.Fd(), r.registry.add(op.Fd(), cb))

	loop := r.loopForFd(op.Fd())
	loop.reqBuss <- subSqeRequest{op, 0, uint64(ud), timeout}

	return ud
}

func (r *NetworkReactor) loopForFd(fd int) *ringNetEventLoop {
	n := len(r.loops)
	return r.loops[fd%n]
}

//Queue io_uring operation.
//Return RequestID which can be used as the SQE identifier.
func (r *NetworkReactor) Queue(op NetOperation, cb Callback) RequestID {
	return r.queue(op, cb, time.Duration(0))
}

//QueueWithDeadline io_uring operation.
//After a deadline time, a CQE with the error ECANCELED will be placed in the callback function.
func (r *NetworkReactor) QueueWithDeadline(op NetOperation, cb Callback, deadline time.Time) RequestID {
	if deadline.IsZero() {
		return r.Queue(op, cb)
	}

	return r.queue(op, cb, time.Until(deadline))
}

//Cancel queued operation.
//id - SQE id returned by Queue method.
func (r *NetworkReactor) Cancel(id RequestID) {
	loop := r.loopForFd(id.fd())
	loop.cancel(id)
}

type cbMap map[uint32]Callback

type registry struct {
	data [][]cbMap

	nonces [][]uint32
	locks  []*sync.Mutex

	granCnt int
}

func newRegistry(granularity int) *registry {
	buff := make([][]cbMap, granularity)
	locks := make([]*sync.Mutex, granularity)
	nl := make([][]uint32, granularity)

	for i := range buff {
		buff[i] = make([]cbMap, 1<<16)
		for j := range buff[i] {
			buff[i][j] = make(cbMap, 10)
		}

		nl[i] = make([]uint32, 1<<16)
		locks[i] = &sync.Mutex{}
	}

	return &registry{
		data:    buff,
		nonces:  nl,
		granCnt: granularity,
		locks:   locks,
	}
}

func (r *registry) add(fd int, cb Callback) uint32 {
	granule := fd % r.granCnt
	idx := fd / r.granCnt

	n := atomic.AddUint32(&r.nonces[granule][fd], 1)

	r.locks[granule].Lock()
	r.data[granule][idx][n] = cb
	r.locks[granule].Unlock()

	return n
}

func (r *registry) pop(fd int, nonce uint32) Callback {
	granule := fd % r.granCnt
	idx := fd / r.granCnt

	r.locks[granule].Lock()
	cb := r.data[granule][idx][nonce]
	delete(r.data[granule][idx], nonce)
	r.locks[granule].Unlock()

	return cb
}

type ringNetEventLoop struct {
	ring *uring.Ring

	registry *registry

	reqBuss      chan subSqeRequest
	submitSignal chan struct{}

	stopReaderChan chan struct{}
	stopWriterChan chan struct{}

	submitAllowed uint32

	log Logger
}

func newRingNetEventLoop(ring *uring.Ring, logger Logger, sockRegistry *registry) *ringNetEventLoop {
	return &ringNetEventLoop{
		ring:           ring,
		reqBuss:        make(chan subSqeRequest, 1<<8),
		submitSignal:   make(chan struct{}),
		stopReaderChan: make(chan struct{}),
		stopWriterChan: make(chan struct{}),
		registry:       sockRegistry,
		log:            logger,
	}
}

func (loop *ringNetEventLoop) runReader(tickDuration time.Duration) {
	//runtime.LockOSThread()

	cqeBuff := make([]*uring.CQEvent, cqeBuffSize)
	for {
		loop.submitSignal <- struct{}{}

		_, err := loop.ring.WaitCQEventsWithTimeout(1, tickDuration)
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.ETIME) {
			runtime.Gosched()
			goto CheckCtxAndContinue
		}

		if err != nil {
			loop.log.Log("io_uring", loop.ring.Fd(), "wait cqe", err)
			goto CheckCtxAndContinue
		}

		loop.submitSignal <- struct{}{}

		for n := loop.ring.PeekCQEventBatch(cqeBuff); n > 0; n = loop.ring.PeekCQEventBatch(cqeBuff) {
			for i := 0; i < n; i++ {
				cqe := cqeBuff[i]

				if cqe.UserData == timeoutNonce || cqe.UserData == cancelNonce {
					continue
				}

				id := RequestID(cqe.UserData)
				cb := loop.registry.pop(id.fd(), id.nonce())
				cb(uring.CQEvent{
					UserData: cqe.UserData,
					Res:      cqe.Res,
					Flags:    cqe.Flags,
				})
			}

			loop.ring.AdvanceCQ(uint32(n))
		}

	CheckCtxAndContinue:
		select {
		case <-loop.stopReaderChan:
			close(loop.stopReaderChan)
			return
		default:
			continue
		}
	}
}

func (loop *ringNetEventLoop) stopReader() {
	loop.stopReaderChan <- struct{}{}
	<-loop.stopReaderChan
}

func (loop *ringNetEventLoop) stopWriter() {
	loop.stopWriterChan <- struct{}{}
	<-loop.stopWriterChan
}

func (loop *ringNetEventLoop) cancel(id RequestID) {
	op := uring.Cancel(uint64(id), 0)

	loop.reqBuss <- subSqeRequest{
		op:       op,
		userData: cancelNonce,
	}
}

func (loop *ringNetEventLoop) runWriter() {
	runtime.LockOSThread()

	defer close(loop.reqBuss)
	defer close(loop.submitSignal)

	var err error
	for {
		select {
		case req := <-loop.reqBuss:
			atomic.StoreUint32(&loop.submitAllowed, 1)

			if req.timeout == 0 {
				err = loop.ring.QueueSQE(req.op, req.flags, req.userData)
			} else {
				err = loop.ring.QueueSQE(req.op, req.flags|uring.SqeIOLinkFlag, req.userData)
				if err == nil {
					err = loop.ring.QueueSQE(uring.LinkTimeout(req.timeout), 0, timeoutNonce)
				}
			}

			if err != nil {
				id := RequestID(req.userData)
				loop.registry.pop(id.fd(), id.nonce())
				loop.log.Log("io_uring", loop.ring.Fd(), "queue operation", err)
			}

		case <-loop.submitSignal:
			if atomic.CompareAndSwapUint32(&loop.submitAllowed, 1, 0) {
				_, err = loop.ring.Submit()
				if err != nil {
					if errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EAGAIN) {
						atomic.StoreUint32(&loop.submitAllowed, 1)
					} else {
						loop.log.Log("io_uring", loop.ring.Fd(), "submit", err)
					}
				}
			}

		case <-loop.stopWriterChan:
			close(loop.stopWriterChan)
			return
		}
	}
}
