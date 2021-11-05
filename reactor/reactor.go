package uring

import (
	"context"
	"fmt"
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

type command struct {
	op  uring.Operation
	cqe chan<- uring.CQEvent
}

type Reactor struct {
	ring *uring.URing

	queueMy sync.Mutex

	commands   map[uint64]*command
	commandsMu sync.RWMutex

	currentNonce uint64
	nonceLock    sync.Mutex

	tickDuration time.Duration
}

type ReactorOption func(r *Reactor)

func WithTickTimeout(duration time.Duration) ReactorOption {
	return func(r *Reactor) {
		r.tickDuration = duration
	}
}

func New(ring *uring.URing, opts ...ReactorOption) *Reactor {
	r := &Reactor{
		ring:         ring,
		commands:     map[uint64]*command{},
		tickDuration: time.Millisecond * 100,
		currentNonce: 100,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

const cqeBatchSize = 1 << 7

func (r *Reactor) Run(ctx context.Context) error {
	for {
		_, err := r.ring.WaitCQEventsWithTimeout(1, r.tickDuration)
		if err == syscall.EAGAIN || err == syscall.EINTR {
			runtime.Gosched()
			continue
		}

		if err != nil && err != syscall.ETIME {
			log.Print("err ", err.Error())
			continue
		}

		for cqes := r.ring.PeekCQEventBatch(cqeBatchSize); len(cqes) > 0; cqes = r.ring.PeekCQEventBatch(cqeBatchSize) {
			for _, cqe := range cqes {
				r.ring.SeenCQE(cqe)

				r.commandsMu.Lock()
				if cqe.UserData == timeoutNonce || cqe.UserData == cancelNonce {
					r.commandsMu.Unlock()
					continue
				}

				cmd := r.commands[cqe.UserData]
				cmd.cqe <- uring.CQEvent{
					UserData: cqe.UserData,
					Res:      cqe.Res,
					Flags:    cqe.Flags,
				}
				delete(r.commands, cqe.UserData)

				r.commandsMu.Unlock()
			}
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

	cmd := &command{
		op:  op,
		cqe: resultChan,
	}

	r.commandsMu.Lock()
	r.commands[nonce] = cmd
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

	_, err = r.ring.Submit()
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

func (r *Reactor) queueSQE(requests ...sqeQueueRequest) (err error) {
	r.queueMy.Lock()
	defer r.queueMy.Unlock()

	for _, req := range requests {
		if qErr := r.ring.QueueSQE(req.op, req.flags, req.userData); qErr != nil {
			if err == nil {
				err = qErr
			} else {
				err = fmt.Errorf("%w; %s", err, qErr.Error())
			}
		}
	}

	return err
}

func (r *Reactor) Cancel(nonce uint64) (err error) {
	err = r.queueSQE(sqeQueueRequest{uring.Cancel(nonce, 0), 0, cancelNonce})
	if err != nil {
		return err
	}

	_, err = r.ring.Submit()
	return err
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
