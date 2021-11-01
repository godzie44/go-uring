package uring

import (
	"context"
	"github.com/godzie44/go-uring/uring"
	"log"
	"runtime"
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

type Reactor struct {
	ring *uring.URing

	commands     map[uint64]uring.Operation
	currentNonce uint64

	result chan Result

	tickDuration time.Duration
}

type ReactorOption func(r *Reactor)

func WithTickTimeout(duration time.Duration) ReactorOption {
	return func(r *Reactor) {
		r.tickDuration = duration
	}
}

func NewReactor(ring *uring.URing, opts ...ReactorOption) *Reactor {
	r := &Reactor{
		ring:         ring,
		result:       make(chan Result),
		commands:     map[uint64]uring.Operation{},
		tickDuration: time.Millisecond * 100,
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
			r.result <- Result{op: r.commands[cqe.UserData], err: cqe.Error()}
			delete(r.commands, cqe.UserData)
			r.ring.SeenCQE(cqe)
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

func (r *Reactor) Execute(ops ...uring.Operation) error {
	for _, op := range ops {
		r.commands[r.currentNonce] = op

		if err := r.ring.QueueSQE(op, 0, r.currentNonce); err != nil {
			return err
		}

		r.currentNonce++
	}

	_, err := r.ring.Submit()
	return err
}

func (r *Reactor) Result() <-chan Result {
	return r.result
}
