package uring

import (
	"context"
	"github.com/godzie44/go-uring/uring"
	"github.com/stretchr/testify/suite"
	"io/ioutil"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

type ReactorTestSuite struct {
	suite.Suite

	defers uring.Defer

	reactor *Reactor

	stopReactor context.CancelFunc
	wg          *sync.WaitGroup
}

func (ts *ReactorTestSuite) SetupTest() {
	rings, defers, err := uring.CreateMany(4, 64)
	ts.Require().NoError(err)
	ts.defers = defers

	ts.reactor, err = New(rings)
	ts.Require().NoError(err)

	ctx, cancel := context.WithCancel(context.Background())
	ts.stopReactor = cancel

	ts.wg = &sync.WaitGroup{}
	ts.wg.Add(2)
	go func() {
		defer ts.wg.Done()
		ts.reactor.Run(ctx)
	}()
	go func() {
		defer ts.wg.Done()
		for err := range ts.reactor.Errors() {
			panic(err)
		}
	}()
}

func (ts *ReactorTestSuite) TearDownTest() {
	ts.stopReactor()
	ts.wg.Wait()
	ts.Require().NoError(ts.defers())
}

func (ts *ReactorTestSuite) TestReactorExecuteReadV() {
	f, err := os.Open("../go.mod")
	ts.Require().NoError(err)
	defer f.Close()

	expected, err := ioutil.ReadFile("../go.mod")
	ts.Require().NoError(err)

	buff := make([]byte, len(expected))

	resultChan := make(chan uring.CQEvent)
	_, err = ts.reactor.Queue(uring.ReadV(f, [][]byte{buff}, 0), resultChan)
	ts.Require().NoError(err)

	cqe := <-resultChan

	ts.Require().Equal(string(expected), string(buff))

	ts.Require().Len(expected, int(cqe.Res))
}

func (ts *ReactorTestSuite) TestExecuteWithDeadline() {
	l, fd, err := makeTCPListener("0.0.0.0:8080")
	ts.Require().NoError(err)
	defer l.Close()

	acceptChan := make(chan uring.CQEvent)

	acceptTime := time.Now()
	_, err = ts.reactor.QueueWithDeadline(uring.Accept(uintptr(fd), 0), acceptTime.Add(time.Second), acceptChan)
	ts.Require().NoError(err)

	cqe := <-acceptChan

	ts.Require().NoError(err)
	ts.Require().Error(cqe.Error(), syscall.ECANCELED)
	ts.Require().True(time.Now().Sub(acceptTime) > time.Second && time.Now().Sub(acceptTime) < time.Second+time.Millisecond*100)
}

func (ts *ReactorTestSuite) TestCancelOperation() {
	l, fd, err := makeTCPListener("0.0.0.0:8080")
	ts.Require().NoError(err)
	defer l.Close()

	acceptChan := make(chan uring.CQEvent)

	nonce, err := ts.reactor.Queue(uring.Accept(uintptr(fd), 0), acceptChan)
	ts.Require().NoError(err)

	go func() {
		<-time.After(time.Second)
		ts.Require().NoError(
			ts.reactor.Cancel(nonce),
		)
	}()

	cqe := <-acceptChan
	ts.Require().Error(cqe.Error(), syscall.ECANCELED)
}

func TestReactor(t *testing.T) {
	suite.Run(t, new(ReactorTestSuite))
}
