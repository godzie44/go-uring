package uring

import (
	"context"
	"fmt"
	"github.com/godzie44/go-uring/uring"
	"math"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	timeoutNonce = math.MaxUint64
	cancelNonce  = math.MaxUint64 - 1

	cqeBuffSize = 1 << 7
)

type RequestID uint64

// expected fd real size is int32
func reqIDFromFdAndType(fd int, opcode uring.OpCode) RequestID {
	return RequestID(uint64(fd) | uint64(opcode)<<32)
}

func (ud RequestID) fd() int {
	var mask = uint64(math.MaxUint32)
	return int(uint64(ud) & mask)
}

func (ud RequestID) opcode() uring.OpCode {
	return uring.OpCode(ud >> 32)
}

type NetworkReactor struct {
	tickDuration time.Duration
	loops        []*ringEventLoop

	errChan chan error
}

type ReactorOption func(r *NetworkReactor)

func WithTickTimeout(duration time.Duration) ReactorOption {
	return func(r *NetworkReactor) {
		r.tickDuration = duration
	}
}

func NewNet(rings []*uring.Ring, opts ...ReactorOption) *NetworkReactor {
	r := &NetworkReactor{
		tickDuration: time.Millisecond * 1,
		errChan:      make(chan error, 128),
	}

	for _, ring := range rings {
		loop := newRingEventLoop(ring, r.errChan, r.tickDuration)
		r.loops = append(r.loops, loop)
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

func (r *NetworkReactor) Run(ctx context.Context) {
	defer close(r.errChan)

	for _, loop := range r.loops {
		go loop.runRingReader()
		go loop.runRingWriter()
	}

	<-ctx.Done()

	for _, loop := range r.loops {
		loop.stopReader()
		loop.stopWriter()
	}

	return
}

func (r *NetworkReactor) Errors() chan error {
	return r.errChan
}

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

func (r *NetworkReactor) queue(op NetOperation, timeout time.Duration) RequestID {
	ud := reqIDFromFdAndType(op.Fd(), op.Code())

	loop := r.LoopForFd(op.Fd())
	loop.reqBuss <- subSqeRequest{op, 0, uint64(ud), timeout}

	return ud
}

func (r *NetworkReactor) LoopForFd(fd int) *ringEventLoop {
	n := len(r.loops)
	return r.loops[fd%n]
}

func (r *NetworkReactor) RegisterFd(fd int, acceptChan, readChan, writeChan chan uring.CQEvent) {
	r.LoopForFd(fd).addFd(fd, acceptChan, readChan, writeChan)
}

func (r *NetworkReactor) Queue(op NetOperation) RequestID {
	return r.queue(op, time.Duration(0))
}

func (r *NetworkReactor) QueueWithDeadline(op NetOperation, deadline time.Time) RequestID {
	if deadline.IsZero() {
		return r.Queue(op)
	}

	return r.queue(op, deadline.Sub(time.Now()))
}

func (r *NetworkReactor) Cancel(id RequestID) {
	loop := r.LoopForFd(id.fd())
	loop.cancel(id)
}

type connection struct {
	fd         int
	acceptChan chan uring.CQEvent
	readChan   chan uring.CQEvent
	writeChan  chan uring.CQEvent
}

type registry []connection

type ringEventLoop struct {
	registry registry

	reqBuss      chan subSqeRequest
	submitSignal chan struct{}

	ring         *uring.Ring
	tickDuration time.Duration

	errChan chan<- error

	stopReaderChan chan struct{}
	stopWriterChan chan struct{}

	needSubmit uint32
}

func newRingEventLoop(ring *uring.Ring, errChan chan<- error, tickDuration time.Duration) *ringEventLoop {
	fmt.Println("r", ring, ring.Fd())

	return &ringEventLoop{
		ring:           ring,
		tickDuration:   tickDuration,
		reqBuss:        make(chan subSqeRequest, 256),
		submitSignal:   make(chan struct{}),
		stopReaderChan: make(chan struct{}),
		stopWriterChan: make(chan struct{}),
		registry:       make(registry, 1<<16),
		errChan:        errChan,
	}
}

func (loop *ringEventLoop) addFd(fd int, acceptChan, readChan, writeChan chan uring.CQEvent) {
	loop.registry[fd].acceptChan = acceptChan
	loop.registry[fd].readChan = readChan
	loop.registry[fd].writeChan = writeChan
}

type RingError struct {
	Err    error
	RingFd int
}

func (r *RingError) Error() string {
	return fmt.Sprintf("%s, ring fd: %d", r.Err.Error(), r.RingFd)
}

type RingQueueError struct {
	RingError
	OpCode uring.OpCode
	ID     uint64
}

func (r *RingQueueError) Error() string {
	return fmt.Sprintf("%s, ring fd: %d", r.Err.Error(), r.RingFd)
}

func (loop *ringEventLoop) runRingReader() {
	runtime.LockOSThread()

	cqeBuff := make([]*uring.CQEvent, cqeBuffSize)
	for {
		loop.submitSignal <- struct{}{}

		_, err := loop.ring.WaitCQEventsWithTimeout(1, loop.tickDuration)
		if err == syscall.EAGAIN || err == syscall.EINTR || err == syscall.ETIME {
			runtime.Gosched()
			goto CheckCtxAndContinue
		}

		if err != nil {
			loop.errChan <- err
			goto CheckCtxAndContinue
		}

		for n := loop.ring.PeekCQEventBatch(cqeBuff); n > 0; n = loop.ring.PeekCQEventBatch(cqeBuff) {
			for i := 0; i < n; i++ {
				cqe := cqeBuff[i]

				if cqe.UserData == timeoutNonce || cqe.UserData == cancelNonce {
					continue
				}

				userData := RequestID(cqe.UserData)
				event := uring.CQEvent{
					UserData: cqe.UserData,
					Res:      cqe.Res,
					Flags:    cqe.Flags,
				}

				switch userData.opcode() {
				case uring.AcceptCode:
					loop.registry[userData.fd()].acceptChan <- event
				case uring.RecvCode:
					loop.registry[userData.fd()].readChan <- event
				case uring.SendCode:
					loop.registry[userData.fd()].writeChan <- event
				}
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

func (loop *ringEventLoop) stopReader() {
	loop.stopReaderChan <- struct{}{}
	<-loop.stopReaderChan
}

func (loop *ringEventLoop) stopWriter() {
	loop.stopWriterChan <- struct{}{}
	<-loop.stopWriterChan
}

func (loop *ringEventLoop) cancel(id RequestID) {
	op := uring.Cancel(uint64(id), 0)

	loop.reqBuss <- subSqeRequest{
		op:       op,
		userData: cancelNonce,
	}
}

func (loop *ringEventLoop) runRingWriter() {
	defer close(loop.reqBuss)
	defer close(loop.submitSignal)

	var err error
	for {
		select {
		case req := <-loop.reqBuss:
			atomic.StoreUint32(&loop.needSubmit, 1)

			if req.timeout == 0 {
				err = loop.ring.QueueSQE(req.op, req.flags, req.userData)
			} else {
				err = loop.ring.QueueSQE(req.op, req.flags|uring.SqeIOLinkFlag, req.userData)
				if err == nil {
					err = loop.ring.QueueSQE(uring.LinkTimeout(req.timeout), 0, timeoutNonce)
				}
			}

			if err != nil {
				loop.errChan <- &RingQueueError{
					RingError{err, loop.ring.Fd()}, req.op.Code(), req.userData,
				}
			}
		case <-loop.submitSignal:
			if atomic.CompareAndSwapUint32(&loop.needSubmit, 1, 0) {
				_, err = loop.ring.Submit()
				if err != nil {
					loop.errChan <- &RingError{err, loop.ring.Fd()}
				}
			}
		case <-loop.stopWriterChan:
			close(loop.stopWriterChan)
			return
		}
	}
}
