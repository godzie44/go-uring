package uring

import (
	"context"
	"github.com/godzie44/go-uring/uring"
	"github.com/stretchr/testify/suite"
	"time"

	"golang.org/x/sys/unix"
	"io/ioutil"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"unsafe"
)

type ReactorTestSuite struct {
	suite.Suite
	ring    *uring.URing
	reactor *Reactor

	stopReactor context.CancelFunc
	wg          *sync.WaitGroup
}

func (ts *ReactorTestSuite) SetupTest() {
	ring, err := uring.New(64)
	ts.Require().NoError(err)
	ts.ring = ring

	ts.reactor = New(ts.ring)

	ctx, cancel := context.WithCancel(context.Background())
	ts.stopReactor = cancel

	ts.wg = &sync.WaitGroup{}
	ts.wg.Add(1)
	go func() {
		defer ts.wg.Done()
		err := ts.reactor.Run(ctx)
		ts.Require().NoError(err)
	}()
}

func (ts *ReactorTestSuite) TearDownTest() {
	ts.stopReactor()
	ts.wg.Wait()

	ts.Require().NoError(ts.ring.Close())
}

func (ts *ReactorTestSuite) TestReactorExecuteReadVCommand() {
	f, err := os.Open("../go.mod")
	ts.Require().NoError(err)
	defer f.Close()

	op, err := uring.ReadV(f, 16)
	ts.Require().NoError(err)

	cqe, err := ts.reactor.ExecuteAndWait(op)
	ts.Require().NoError(err)

	reads := op
	expected, err := ioutil.ReadFile("../go.mod")
	ts.Require().NoError(err)

	str := string(unsafe.Slice(reads.IOVecs[0].Base, reads.Size))
	ts.Require().Equal(string(expected), str)

	ts.Require().Len(expected, int(cqe.Res))
}

func (ts *ReactorTestSuite) TestReactorExecuteWithDeadline() {
	l, fd, err := makeTCPListener("0.0.0.0:8080")
	ts.Require().NoError(err)
	defer l.Close()

	acceptTime := time.Now()
	cqe, err := ts.reactor.ExecuteAndWaitWithDeadline(uring.Accept(fd, 0), acceptTime.Add(time.Second))

	ts.Require().Error(err, syscall.ETIME)
	ts.Require().Error(cqe.Error(), syscall.ECANCELED)
	ts.Require().True(time.Now().Sub(acceptTime) > time.Second && time.Now().Sub(acceptTime) < time.Second+time.Millisecond*100)
}

func TestReactor(t *testing.T) {
	suite.Run(t, new(ReactorTestSuite))
}

func makeTCPListener(addr string) (*net.TCPListener, uintptr, error) {
	var fdescr uintptr

	var listenConfig = net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			_ = c.Control(func(fd uintptr) {
				if err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
					return
				}
				if err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
					return
				}
				if err = syscall.SetNonblock(int(fd), false); err != nil {
					return
				}
				fdescr = fd
			})
			return err
		},
	}

	conn, err := listenConfig.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fdescr, err
	}

	return conn.(*net.TCPListener), fdescr, err
}
