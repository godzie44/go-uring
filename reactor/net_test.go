//go:build linux

package reactor

import (
	"context"
	"github.com/godzie44/go-uring/uring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
	"math"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

type NetworkReactorTestSuite struct {
	suite.Suite

	defers uring.Defer

	reactor *NetworkReactor

	stopReactor context.CancelFunc
	wg          *sync.WaitGroup
}

func (ts *NetworkReactorTestSuite) SetupTest() {
	rings, defers, err := uring.CreateMany(4, 64, 4)
	ts.Require().NoError(err)
	ts.defers = defers

	ts.reactor, err = NewNet(rings)
	ts.Require().NoError(err)

	ctx, cancel := context.WithCancel(context.Background())
	ts.stopReactor = cancel

	ts.wg = &sync.WaitGroup{}
	ts.wg.Add(1)
	go func() {
		defer ts.wg.Done()
		ts.reactor.Run(ctx)
	}()
}

func (ts *NetworkReactorTestSuite) TearDownTest() {
	ts.stopReactor()
	ts.wg.Wait()
	ts.Require().NoError(ts.defers())
}

func (ts *NetworkReactorTestSuite) TestExecuteWithDeadline() {
	l, fd, err := makeTCPListener("0.0.0.0:8080")
	ts.Require().NoError(err)
	defer l.Close()

	acceptChan := make(chan uring.CQEvent)

	acceptTime := time.Now()
	_ = ts.reactor.QueueWithDeadline(uring.Accept(uintptr(fd), 0), func(event uring.CQEvent) {
		acceptChan <- event
	}, acceptTime.Add(time.Second))

	cqe := <-acceptChan

	ts.Require().NoError(err)
	ts.Require().Error(cqe.Error(), syscall.ECANCELED)
	ts.Require().True(time.Since(acceptTime) > time.Second && time.Since(acceptTime) < time.Second+time.Millisecond*100)
}

func (ts *NetworkReactorTestSuite) TestCancelOperation() {
	l, fd, err := makeTCPListener("0.0.0.0:8080")
	ts.Require().NoError(err)
	defer l.Close()

	acceptChan := make(chan uring.CQEvent)
	id := ts.reactor.Queue(uring.Accept(uintptr(fd), 0), func(event uring.CQEvent) {
		acceptChan <- event
	})

	go func() {
		<-time.After(time.Second)
		ts.reactor.Cancel(id)
	}()

	cqe := <-acceptChan
	ts.Require().Error(cqe.Error(), syscall.ECANCELED)
}

func TestNetworkReactor(t *testing.T) {
	suite.Run(t, new(NetworkReactorTestSuite))
}

func TestRequestID(t *testing.T) {
	type testCase struct {
		fd    int
		nonce uint32
	}
	testCases := []testCase{
		{fd: 1, nonce: 1},
		{fd: 2, nonce: 2},
		{fd: 32600, nonce: math.MaxUint32 >> 2},
		{fd: 128000, nonce: math.MaxUint32 >> 1},
		{fd: math.MaxInt32, nonce: math.MaxUint32},
	}

	for _, tc := range testCases {
		ud := packRequestID(tc.fd, tc.nonce)
		assert.GreaterOrEqual(t, uint64(ud), uint64(math.MaxUint32))
		assert.Equal(t, tc.fd, ud.fd())
		assert.GreaterOrEqual(t, tc.nonce, ud.nonce())
	}
}

func makeTCPListener(addr string) (*net.TCPListener, int, error) {
	var fdescr int

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
				fdescr = int(fd)
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
