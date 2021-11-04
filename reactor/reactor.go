package uring

import (
	"context"
	"errors"
	"fmt"
	"github.com/godzie44/go-uring/uring"
	"log"
	"runtime"
	"sync"
	"syscall"
	"time"
)

type Result struct {
	err error
	op  uring.Operation
}

func (r *Result) Error() error {
	return r.err
}

func (r *Result) Operation() uring.Operation {
	return r.op
}

type command struct {
	op  uring.Operation
	cqe chan uring.CQEvent
}

type Reactor struct {
	ring *uring.URing

	queueMy sync.Mutex

	commands   map[uint64]*command
	commandsMu sync.RWMutex

	currentNonce uint64
	nonceLock    sync.Mutex

	result chan Result

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
		result:       make(chan Result),
		commands:     map[uint64]*command{},
		tickDuration: time.Millisecond * 1000,
		currentNonce: 100,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

func (r *Reactor) Run(ctx context.Context) error {
	defer close(r.result)

	for {
		cqe, err := r.ring.WaitCQEventsWithTimeout(1, r.tickDuration)
		if err == syscall.EAGAIN || err == syscall.EINTR {
			runtime.Gosched()
			continue
		}

		if err == nil {
			//r.result <- Result{op: r.commands[cqe.UserData], err: cqe.Error()}
			r.ring.SeenCQE(cqe)

			r.commandsMu.Lock()
			chn := r.commands[cqe.UserData].cqe

			chn <- uring.CQEvent{
				UserData: cqe.UserData,
				Res:      cqe.Res,
				Flags:    cqe.Flags,
			}
			delete(r.commands, cqe.UserData)

			r.commandsMu.Unlock()
		}

		if err != nil && err != syscall.ETIME {
			log.Print("err ", err.Error())
		}

		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
}

func (r *Reactor) ExecuteAndWait(op uring.Operation) (uring.CQEvent, error) {
	nonce := r.nextNonce()

	cqeChan := make(chan uring.CQEvent)
	defer close(cqeChan)

	cmd := &command{
		op:  op,
		cqe: cqeChan,
	}

	r.commandsMu.Lock()
	r.commands[nonce] = cmd
	r.commandsMu.Unlock()

	if err := r.queueSQE(sqeQueueRequest{op, 0, nonce}); err != nil {
		return uring.CQEvent{}, err
	}

	if _, err := r.ring.Submit(); err != nil {
		return uring.CQEvent{}, err
	}

	return <-cqeChan, nil
}

func (r *Reactor) ExecuteAndWaitWithDeadline(op uring.Operation, deadline time.Time) (uring.CQEvent, error) {
	if deadline.IsZero() {
		return r.ExecuteAndWait(op)
	}

	opNonce := r.nextNonce()
	timeoutNonce := r.nextNonce()

	cqeChan := make(chan uring.CQEvent, 2)
	defer close(cqeChan)

	cmd := &command{
		op:  op,
		cqe: cqeChan,
	}
	linkTimeoutCmd := &command{
		op:  uring.LinkTimeout(deadline.Sub(time.Now())),
		cqe: cqeChan,
	}

	r.commandsMu.Lock()
	r.commands[opNonce] = cmd
	r.commands[timeoutNonce] = linkTimeoutCmd
	r.commandsMu.Unlock()

	err := r.queueSQE(
		sqeQueueRequest{op, uring.SqeIOLinkFlag, opNonce},
		sqeQueueRequest{linkTimeoutCmd.op, 0, timeoutNonce},
	)
	if err != nil {
		return uring.CQEvent{}, err
	}

	if _, err = r.ring.Submit(); err != nil {
		return uring.CQEvent{}, err
	}

	opCqe, timeoutCqe := <-cqeChan, <-cqeChan
	if opCqe.UserData != opNonce {
		opCqe, timeoutCqe = timeoutCqe, opCqe
	}

	if timeoutCqe.Error() != nil && !errors.Is(timeoutCqe.Error(), syscall.ECANCELED) {
		return opCqe, timeoutCqe.Error()
	}
	return opCqe, nil
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

func (r *Reactor) nextNonce() uint64 {
	r.nonceLock.Lock()
	r.currentNonce++
	nonce := r.currentNonce
	r.nonceLock.Unlock()
	return nonce
}

func (r *Reactor) Result() <-chan Result {
	return r.result
}
