package uring

import (
	"context"
	"github.com/godzie44/go-uring/uring"
	"log"
	"math"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const (
	timeoutNonce = math.MaxUint64
	cancelNonce  = math.MaxUint64 - 1
)

type OperationCache map[string]uring.Operation

func NewOperationCache() OperationCache {
	return make(OperationCache)
}

func (o OperationCache) GetOrSet(key string, setFn func() uring.Operation) uring.Operation {
	v, exists := o[key]
	if !exists {
		v = setFn()
		o[key] = v
	}

	return v
}

type command struct {
	op  uring.Operation
	cqe chan<- uring.CQEvent
}

type Reactor struct {
	ring *uring.Ring

	queueMy sync.Mutex

	commands   map[uint64]command
	commandsMu sync.RWMutex

	//commandsArr   [math.MaxUint32]int
	//commandsMu sync.RWMutex

	reqBuss chan sqeQueueRequest

	currentNonce uint64
	nonceLock    sync.Mutex

	buffer           []sqeQueueRequest
	submitBufferLock sync.Mutex

	tickDuration time.Duration

	opCache OperationCache
}

type ReactorOption func(r *Reactor)

func WithTickTimeout(duration time.Duration) ReactorOption {
	return func(r *Reactor) {
		r.tickDuration = duration
	}
}

func New(ring *uring.Ring, opts ...ReactorOption) *Reactor {
	r := &Reactor{
		ring:         ring,
		commands:     map[uint64]command{},
		tickDuration: time.Millisecond,
		currentNonce: 1,
		opCache:      NewOperationCache(),
		reqBuss:      make(chan sqeQueueRequest, 128),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

func (r *Reactor) SubmitSQELoop() {
	for req := range r.reqBuss {
		_ = r.ring.QueueSQE(req.op, req.flags, req.userData)
	}
}

const cqeBatchSize = 1 << 7

func (r *Reactor) Run(ctx context.Context) error {
	go r.SubmitSQELoop()

	cqeBuff := make([]*uring.CQEvent, cqeBatchSize)

	for {
		_, _ = r.ring.Submit()

		_, err := r.ring.WaitCQEventsWithTimeout(1, r.tickDuration)
		if err == syscall.EAGAIN || err == syscall.EINTR {
			runtime.Gosched()
			continue
		}

		if err != nil && err != syscall.ETIME {
			log.Print("err ", err.Error())
			continue
		}

		for n := r.ring.PeekCQEventBatch(cqeBuff); n > 0; n = r.ring.PeekCQEventBatch(cqeBuff) {
			for i := 0; i < n; i++ {
				cqe := cqeBuff[i]

				//r.ring.SeenCQE(cqe)
				if cqe.UserData == timeoutNonce || cqe.UserData == cancelNonce {
					continue
				}

				r.commandsMu.Lock()

				cmd := r.commands[cqe.UserData]
				//if !exists {
				//	//fmt.Println("NOT EXISTING DATA", cqe.UserData, "cntr", cntr)
				//	//_, exists := deletes[cqe.UserData]
				//	//if exists {
				//	//	fmt.Println("DELETE PREVIOUSLY!!!", "cntr", deletes[cqe.UserData])
				//	//}
				//	//fmt.Println("res", cqe.Res)
				//	//fmt.Println("len", len(r.commands))
				//	//fmt.Println("len", r.commands)
				//
				//}

				cmd.cqe <- uring.CQEvent{
					UserData: cqe.UserData,
					Res:      cqe.Res,
					Flags:    cqe.Flags,
				}
				delete(r.commands, cqe.UserData)

				r.commandsMu.Unlock()
			}

			r.ring.AdvanceCQ(uint32(n))
		}

		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
}

func (r *Reactor) queue(op uring.Operation, resultChan chan<- uring.CQEvent, linkedReqs ...sqeQueueRequest) (nonce uint64, err error) {
	nonce = r.nextNonce()

	r.commandsMu.Lock()
	r.commands[nonce] = command{
		op:  op,
		cqe: resultChan,
	}
	r.commandsMu.Unlock()

	if len(linkedReqs) == 0 {
		err = r.queueSQE(sqeQueueRequest{op, 0, nonce})
	} else {
		request := []sqeQueueRequest{
			{op, uring.SqeIOLinkFlag, nonce},
		}
		err = r.queueSQE(append(request, linkedReqs...)...)
	}

	if err != nil {
		return nonce, err
	}

	return nonce, err
}

func (r *Reactor) Queue(op uring.Operation, resultChan chan<- uring.CQEvent) (nonce uint64, err error) {
	return r.queue(op, resultChan)
}

func (r *Reactor) QueueWithDeadline(op uring.Operation, deadline time.Time, resultChan chan<- uring.CQEvent) (nonce uint64, err error) {
	if deadline.IsZero() {
		return r.Queue(op, resultChan)
	}

	return r.queue(op, resultChan, sqeQueueRequest{
		op:       uring.LinkTimeout(deadline.Sub(time.Now())),
		userData: timeoutNonce,
	})
}

func (r *Reactor) QueueAndWait(op uring.Operation) (uring.CQEvent, error) {
	cqeChan := make(chan uring.CQEvent)
	defer close(cqeChan)

	_, err := r.Queue(op, cqeChan)
	if err != nil {
		return uring.CQEvent{}, err
	}

	return <-cqeChan, nil
}

func (r *Reactor) QueueAndWaitWithDeadline(op uring.Operation, deadline time.Time) (uring.CQEvent, error) {
	cqeChan := make(chan uring.CQEvent)
	defer close(cqeChan)

	_, err := r.QueueWithDeadline(op, deadline, cqeChan)
	if err != nil {
		return uring.CQEvent{}, err
	}

	return <-cqeChan, nil
}

type sqeQueueRequest struct {
	op       uring.Operation
	flags    uint8
	userData uint64
}

func (r *Reactor) queueSQEnew(requests ...sqeQueueRequest) (err error) {
	r.submitBufferLock.Lock()
	defer r.submitBufferLock.Unlock()

	r.buffer = append(r.buffer, requests...)
	return nil
}

func (r *Reactor) queueSQE(requests ...sqeQueueRequest) (err error) {
	for _, req := range requests {
		r.reqBuss <- req
	}
	return nil
	//
	//r.queueMy.Lock()
	//defer r.queueMy.Unlock()
	//
	//for _, req := range requests {
	//	if qErr := r.ring.QueueSQE(req.op, req.flags, req.userData); qErr != nil {
	//		if err == nil {
	//			err = qErr
	//		} else {
	//			err = fmt.Errorf("%w; %s", err, qErr.Error())
	//		}
	//	}
	//}
	//
	//if err != nil {
	//	return err
	//}
	//
	//_, err = r.ring.Submit()
	//return err
}

func (r *Reactor) Cancel(nonce uint64) (err error) {
	op := r.opCache.GetOrSet("cancel", func() uring.Operation {
		return uring.Cancel(0, 0)
	})
	op.(*uring.CancelOp).SetTargetUserData(nonce)

	return r.queueSQE(sqeQueueRequest{op, 0, cancelNonce})
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
