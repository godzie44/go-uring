package uring

import (
	"context"
	"errors"
	"github.com/godzie44/go-uring/uring"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Reactor struct {
	tickDuration time.Duration
	loops        []*ringEventLoop2

	currentNonce uint64
	nonceLock    sync.Mutex

	errChan chan error
}

func New(rings []*uring.Ring, opts ...ReactorOption) *Reactor {
	r := &Reactor{
		tickDuration: time.Millisecond * 1,
		errChan:      make(chan error, 128),
	}

	for _, ring := range rings {
		loop := newRingEventLoop2(ring, r.errChan, r.tickDuration)
		r.loops = append(r.loops, loop)
	}

	return r
}

func (r *Reactor) Run(ctx context.Context) {
	defer close(r.errChan)

	for _, loop := range r.loops {
		go loop.runReader()
		go loop.runWriter()
	}

	<-ctx.Done()

	for _, loop := range r.loops {
		loop.stopReader()
		loop.stopWriter()
	}

	return
}

func (r *Reactor) Errors() chan error {
	return r.errChan
}

func (r *Reactor) queue(op uring.Operation, timeout time.Duration, retChan chan uring.CQEvent) (uint64, error) {
	nonce := r.nextNonce()

	loop := r.LoopForNonce(nonce)

	err := loop.Queue(subSqeRequest{op, 0, nonce, timeout}, retChan)

	return nonce, err
}

func (r *Reactor) LoopForNonce(nonce uint64) *ringEventLoop2 {
	n := len(r.loops)
	return r.loops[nonce%uint64(n)]
}

func (r *Reactor) Queue(op uring.Operation, retChan chan uring.CQEvent) (uint64, error) {
	return r.queue(op, time.Duration(0), retChan)
}

func (r *Reactor) QueueWithDeadline(op uring.Operation, deadline time.Time, retChan chan uring.CQEvent) (uint64, error) {
	if deadline.IsZero() {
		return r.Queue(op, retChan)
	}

	return r.queue(op, deadline.Sub(time.Now()), retChan)
}

func (r *Reactor) Cancel(nonce uint64) error {
	loop := r.LoopForNonce(nonce)
	return loop.cancel(nonce)
}

type ringEventLoop2 struct {
	returns     map[uint64]chan uring.CQEvent
	returnsLock sync.Mutex

	queueSQELock sync.Mutex

	submitSignal chan struct{}

	ring         *uring.Ring
	tickDuration time.Duration

	errChan chan<- error

	stopReaderChan chan struct{}
	stopWriterChan chan struct{}

	needSubmit uint32
}

func newRingEventLoop2(ring *uring.Ring, errChan chan<- error, tickDuration time.Duration) *ringEventLoop2 {
	return &ringEventLoop2{
		ring:           ring,
		tickDuration:   tickDuration,
		submitSignal:   make(chan struct{}),
		stopReaderChan: make(chan struct{}),
		stopWriterChan: make(chan struct{}),
		returns:        map[uint64]chan uring.CQEvent{},
		errChan:        errChan,
	}
}

func (loop *ringEventLoop2) runReader() {
	runtime.LockOSThread()

	cqeBuff := make([]*uring.CQEvent, cqeBuffSize)
	for {
		loop.submitSignal <- struct{}{}

		_, err := loop.ring.WaitCQEventsWithTimeout(1, loop.tickDuration)

		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.ETIME) {
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

				nonce := cqe.UserData
				if nonce == timeoutNonce || nonce == cancelNonce {
					continue
				}

				loop.returnsLock.Lock()
				loop.returns[nonce] <- uring.CQEvent{
					UserData: cqe.UserData,
					Res:      cqe.Res,
					Flags:    cqe.Flags,
				}
				delete(loop.returns, nonce)
				loop.returnsLock.Unlock()
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

func (loop *ringEventLoop2) stopReader() {
	loop.stopReaderChan <- struct{}{}
	<-loop.stopReaderChan
}

func (loop *ringEventLoop2) stopWriter() {
	loop.stopWriterChan <- struct{}{}
	<-loop.stopWriterChan
}

func (loop *ringEventLoop2) cancel(nonce uint64) error {
	op := uring.Cancel(nonce, 0)

	return loop.Queue(subSqeRequest{
		op:       op,
		userData: cancelNonce,
	}, nil)
}

func (loop *ringEventLoop2) Queue(req subSqeRequest, retChan chan uring.CQEvent) (err error) {
	loop.queueSQELock.Lock()
	defer loop.queueSQELock.Unlock()

	atomic.StoreUint32(&loop.needSubmit, 1)

	if req.timeout == 0 {
		err = loop.ring.QueueSQE(req.op, req.flags, req.userData)
	} else {
		err = loop.ring.QueueSQE(req.op, req.flags|uring.SqeIOLinkFlag, req.userData)
		if err == nil {
			_ = loop.ring.QueueSQE(uring.LinkTimeout(req.timeout), 0, timeoutNonce)
		}
	}

	if err == nil {
		loop.returnsLock.Lock()
		loop.returns[req.userData] = retChan
		loop.returnsLock.Unlock()
	}

	return err
}

func (loop *ringEventLoop2) runWriter() {
	defer close(loop.submitSignal)

	var err error
	for {
		select {
		case <-loop.submitSignal:
			if atomic.CompareAndSwapUint32(&loop.needSubmit, 1, 0) {
				loop.queueSQELock.Lock()
				_, err = loop.ring.Submit()
				loop.queueSQELock.Unlock()

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

func (r *Reactor) nextNonce() uint64 {
	r.nonceLock.Lock()
	defer r.nonceLock.Unlock()

	r.currentNonce++
	if r.currentNonce >= cancelNonce {
		r.currentNonce = 0
	}

	return r.currentNonce
}
