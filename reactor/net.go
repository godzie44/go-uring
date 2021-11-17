//go:build linux

package uring

import (
	"context"
	"errors"
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

//RequestID identifier of SQE queued into NetworkReactor.
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

//NetworkReactor is event loop's manager with main responsibility - handling client requests and return responses asynchronously.
//NetworkReactor optimized for network operations like Accept, Recv, Send.
type NetworkReactor struct {
	tickDuration time.Duration
	loops        []*ringNetEventLoop

	registry *registry

	errChan chan error
}

func (r *NetworkReactor) setTickDuration(duration time.Duration) {
	r.tickDuration = duration
}

//NewNet create NetworkReactor instance.
func NewNet(rings []*uring.Ring, opts ...ReactorOption) (*NetworkReactor, error) {
	for _, ring := range rings {
		if err := checkRingReq(ring, true); err != nil {
			return nil, err
		}
	}

	r := &NetworkReactor{
		tickDuration: time.Millisecond * 1,
		errChan:      make(chan error, 128),
	}

	r.registry = newRegistry(len(rings))

	for _, ring := range rings {
		loop := newRingNetEventLoop(ring, r.errChan, r.registry, r.tickDuration)
		r.loops = append(r.loops, loop)
	}

	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

//Run start NetworkReactor.
func (r *NetworkReactor) Run(ctx context.Context) {
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
}

func (r *NetworkReactor) Errors() chan error {
	return r.errChan
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

func (r *NetworkReactor) queue(op NetOperation, timeout time.Duration) RequestID {
	ud := reqIDFromFdAndType(op.Fd(), op.Code())

	loop := r.loopForFd(op.Fd())
	loop.reqBuss <- subSqeRequest{op, 0, uint64(ud), timeout}

	return ud
}

func (r *NetworkReactor) loopForFd(fd int) *ringNetEventLoop {
	n := len(r.loops)
	return r.loops[fd%n]
}

//RegisterSocket this method must be called for all sockets interacting with the NetworkReactor.
//fd - socket file descriptor.
//readChan - result channel for read operations (Accept, Recv).
//writeChan - result channel for write operations (Send).
func (r *NetworkReactor) RegisterSocket(fd int, readChan, writeChan chan<- uring.CQEvent) {
	r.registry.add(fd, readChan, writeChan)
}

//Queue io_uring operation.
//Return RequestID which can be used as the SQE identifier.
func (r *NetworkReactor) Queue(op NetOperation) RequestID {
	return r.queue(op, time.Duration(0))
}

//QueueWithDeadline io_uring operation.
//After a deadline time, a CQE with the error ECANCELED will be placed in the channel retChan.
func (r *NetworkReactor) QueueWithDeadline(op NetOperation, deadline time.Time) RequestID {
	if deadline.IsZero() {
		return r.Queue(op)
	}

	return r.queue(op, time.Until(deadline))
}

//Cancel queued operation.
//id - SQE id returned by Queue method.
func (r *NetworkReactor) Cancel(id RequestID) {
	loop := r.loopForFd(id.fd())
	loop.cancel(id)
}

type sockInfo struct {
	fd        int
	readChan  chan<- uring.CQEvent
	writeChan chan<- uring.CQEvent
}

type registry struct {
	data    [][]sockInfo
	granCnt int
}

func newRegistry(granularity int) *registry {
	buff := make([][]sockInfo, granularity)
	for i := range buff {
		buff[i] = make([]sockInfo, 1<<16)
	}
	return &registry{
		data:    buff,
		granCnt: granularity,
	}
}

func (r *registry) add(fd int, readChan chan<- uring.CQEvent, writeChan chan<- uring.CQEvent) {
	granule := fd % r.granCnt
	idx := fd / r.granCnt

	r.data[granule][idx].fd = fd
	r.data[granule][idx].readChan = readChan
	r.data[granule][idx].writeChan = writeChan
}

func (r *registry) get(fd int) *sockInfo {
	granule := fd % r.granCnt
	idx := fd / r.granCnt

	return &r.data[granule][idx]
}

type ringNetEventLoop struct {
	fdDivider int

	registry *registry

	reqBuss      chan subSqeRequest
	submitSignal chan struct{}

	ring         *uring.Ring
	tickDuration time.Duration

	errChan chan<- error

	stopReaderChan chan struct{}
	stopWriterChan chan struct{}

	needSubmit uint32
}

func newRingNetEventLoop(ring *uring.Ring, errChan chan<- error, sockRegistry *registry, tickDuration time.Duration) *ringNetEventLoop {
	return &ringNetEventLoop{
		ring:           ring,
		tickDuration:   tickDuration,
		reqBuss:        make(chan subSqeRequest, 256),
		submitSignal:   make(chan struct{}),
		stopReaderChan: make(chan struct{}),
		stopWriterChan: make(chan struct{}),
		registry:       sockRegistry,
		errChan:        errChan,
	}
}

func (loop *ringNetEventLoop) addFd(fd int, readChan, writeChan chan<- uring.CQEvent) {
	loop.registry.add(fd, readChan, writeChan)
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

func (loop *ringNetEventLoop) runReader() {
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

				if cqe.UserData == timeoutNonce || cqe.UserData == cancelNonce {
					continue
				}

				id := RequestID(cqe.UserData)
				event := uring.CQEvent{
					UserData: cqe.UserData,
					Res:      cqe.Res,
					Flags:    cqe.Flags,
				}

				sock := loop.registry.get(id.fd())

				switch id.opcode() {
				case uring.AcceptCode, uring.RecvCode:
					sock.readChan <- event
				case uring.SendCode:
					sock.writeChan <- event
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
