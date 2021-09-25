package uring

import (
	"context"
	"log"
	"runtime"
	"syscall"
	"time"
)

type Command interface {
	SetUserData(v uint64)
	UserData() uint64
	fillSQE(*SQEntry)
}

type Result struct {
	err error
	cmd Command
}

func (r *Result) Error() error {
	return r.err
}

func (r *Result) Command() Command {
	return r.cmd
}

type Reactor struct {
	ring *URing

	commands     map[uint64]Command
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

func NewReactor(ring *URing, opts ...ReactorOption) *Reactor {
	r := &Reactor{
		ring:         ring,
		result:       make(chan Result),
		commands:     map[uint64]Command{},
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
			r.result <- Result{cmd: r.commands[cqe.UserData], err: cqe.Error()}
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

func (r *Reactor) Execute(commands ...Command) error {
	for _, cmd := range commands {
		cmd.SetUserData(r.currentNonce)
		r.commands[r.currentNonce] = cmd

		if err := r.ring.FillNextSQE(cmd.fillSQE); err != nil {
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
