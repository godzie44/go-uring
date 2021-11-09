package uring

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

func dial(t *testing.T, connChan chan<- net.Conn) {
	c, err := net.Dial("tcp", "0.0.0.0:8080")
	require.NoError(t, err)

	connChan <- c
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

const sendData = "hello world"

// Test that IORING_OP_ACCEPT works.
func TestAccept(t *testing.T) {
	ring, err := New(64)
	require.NoError(t, err)
	defer ring.Close()

	tcpListener, listenerFd, err := makeTCPListener("0.0.0.0:8080")
	require.NoError(t, err)
	defer tcpListener.Close()

	clientConnChan := make(chan net.Conn)
	go dial(t, clientConnChan)

	connFd, err := acceptConnection(t, ring, listenerFd)
	require.NoError(t, err)

	clientConn := <-clientConnChan
	require.NoError(t, err)
	clientConnFile, err := clientConn.(*net.TCPConn).File()
	require.NoError(t, err)

	require.NoError(t, queueSend(ring, clientConnFile.Fd(), []byte(sendData)))

	readBuff := make([]byte, 100)
	require.NoError(t, queueRecv(ring, connFd, readBuff))

	_, err = ring.SubmitAndWaitCQEvents(2)
	require.NoError(t, err)

	events := make([]*CQEvent, 2)
	eventCnt := ring.PeekCQEventBatch(events)
	require.Equal(t, 2, eventCnt)
	for _, cqe := range events[:eventCnt] {
		assert.NoError(t, cqe.Error())

		switch cqe.UserData {
		case 1:
			assert.Equal(t, len(sendData), int(cqe.Res))
		case 2:
			assert.Equal(t, len(sendData), int(cqe.Res))
			assert.Equal(t, []byte(sendData), readBuff[:cqe.Res])
		}
		ring.SeenCQE(cqe)
	}
}

func acceptConnection(t *testing.T, ring *URing, fd uintptr) (cfd uintptr, err error) {
	err = ring.QueueSQE(Accept(fd, 0), 0, 0)
	require.NoError(t, err)

	_, err = ring.Submit()
	require.NoError(t, err)

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)

	ring.SeenCQE(cqe)

	return uintptr(cqe.Res), cqe.Error()
}

func queueSend(ring *URing, fd uintptr, buff []byte) error {
	op := &WriteVOp{FD: fd, IOVecs: []syscall.Iovec{
		{
			Base: &buff[0],
			Len:  uint64(len(buff)),
		},
	}, Offset: 0}
	return ring.QueueSQE(op, 0, 1)
}

func queueRecv(ring *URing, fd uintptr, buff []byte) error {
	op := &ReadVOp{FD: fd, IOVecs: []syscall.Iovec{
		{
			Base: &buff[0],
			Len:  uint64(len(buff)),
		},
	}}
	return ring.QueueSQE(op, 0, 2)
}

// Test we can cancel IORING_OP_ACCEPT.
func TestAcceptCancel(t *testing.T) {
	type testCase struct {
		cancelDelay time.Duration
	}
	testCases := []testCase{
		{0},
		{time.Microsecond * 10000},
	}

	for _, tc := range testCases {
		ring, err := New(32)
		require.NoError(t, err)

		tcpListener, listenerFd, err := makeTCPListener("0.0.0.0:8080")
		require.NoError(t, err)

		require.NoError(t, ring.QueueSQE(Accept(listenerFd, 0), 0, 1))

		_, err = ring.Submit()
		require.NoError(t, err)

		if tc.cancelDelay != 0 {
			time.Sleep(tc.cancelDelay)
		}

		require.NoError(t, ring.QueueSQE(Cancel(1, 0), 0, 2))
		_, err = ring.Submit()
		require.NoError(t, err)

		for i := 0; i < 2; i++ {
			cqe, err := ring.WaitCQEvents(1)
			require.NoError(t, err)

			if cqe.UserData == 1 {
				assert.True(t, cqe.Error() == syscall.EINTR || cqe.Error() == syscall.ECANCELED)
			} else if cqe.UserData == 2 {
				assert.True(t, cqe.Error() == syscall.EALREADY || cqe.Error() == nil)
			}
			ring.SeenCQE(cqe)
		}

		require.NoError(t, ring.Close())
		require.NoError(t, tcpListener.Close())
	}
}

//Test issue many accepts and see if we handle cancellation on exit
func TestAcceptMany(t *testing.T) {
	type testCase struct {
		count     int
		delay     time.Duration
		firstPort int
	}
	testCases := []testCase{
		{count: 128, delay: 0, firstPort: 8090},
		{count: 128, delay: time.Microsecond * 10000, firstPort: 8090},
	}

	for _, tc := range testCases {
		ring, err := New(uint32(2 * tc.count))
		require.NoError(t, err)

		listeners := make([]*net.TCPListener, 0, tc.count)
		for i := 0; i < tc.count; i++ {
			tcpListener, listenerFd, err := makeTCPListener(fmt.Sprintf("0.0.0.0:%d", tc.firstPort+i))
			require.NoError(t, err)

			listeners = append(listeners, tcpListener)

			err = ring.QueueSQE(Accept(listenerFd, 0), 0, 0)
			require.NoError(t, err)
		}

		submitted, err := ring.Submit()
		require.NoError(t, err)
		assert.Equal(t, uint(tc.count), submitted)

		if tc.delay != 0 {
			time.Sleep(tc.delay)
		}

		_, err = ring.PeekCQE()
		assert.True(t, err == syscall.EAGAIN)

		for _, l := range listeners {
			assert.NoError(t, l.Close())
		}

		require.NoError(t, ring.Close())
	}
}

type linkTestCase struct {
	doConnect bool
	timeout   time.Duration
	expected  [2]error
}

//TestAcceptLink test accept with linked IORING_OP_LINK_TIMEOUT sqe.
func TestAcceptLink(t *testing.T) {
	testCases := []linkTestCase{
		{doConnect: true, timeout: time.Second, expected: [2]error{nil, syscall.ECANCELED}},
	}

	ring, err := New(1)
	require.NoError(t, err)

	if ring.Params.FastPollFeature() {
		testCases = append(testCases, linkTestCase{doConnect: false, timeout: time.Millisecond * 200, expected: [2]error{syscall.ECANCELED, syscall.ETIME}})
	} else {
		testCases = append(testCases, linkTestCase{doConnect: false, timeout: time.Millisecond * 200, expected: [2]error{syscall.EINTR, syscall.EALREADY}})
	}
	require.NoError(t, ring.Close())

	for _, tc := range testCases {
		syncChan := make(chan struct{}, 2)

		wg := &sync.WaitGroup{}

		wg.Add(1)
		go func(tc linkTestCase) {
			defer wg.Done()
			recvG(t, 8080, tc, syncChan)
		}(tc)

		if tc.doConnect {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sendG(t, 8080, syncChan)
			}()
		}

		wg.Wait()
	}
}

func recvG(t *testing.T, port int, tc linkTestCase, syncChan chan struct{}) {
	ring, err := New(8)
	require.NoError(t, err)

	tcpListener, listenerFd, err := makeTCPListener(fmt.Sprintf("0.0.0.0:%d", port))
	require.NoError(t, err)
	defer tcpListener.Close()

	syncChan <- struct{}{}

	err = ring.QueueSQE(Accept(listenerFd, 0), SqeIOLinkFlag, 1)
	require.NoError(t, err)

	err = ring.QueueSQE(LinkTimeout(tc.timeout), 0, 2)
	require.NoError(t, err)

	_, err = ring.Submit()
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		cqe, err := ring.WaitCQEvents(1)
		require.NoError(t, err)

		idx := cqe.UserData - 1
		if cqe.Error() != tc.expected[idx] {
			if cqe.Error() == syscall.EBADFD {
				t.Skipf("Skipped, accept not supported")
			}
			t.Fatalf("expected err: %s, got: %s", tc.expected[idx], cqe.Error())
		}

		ring.SeenCQE(cqe)
	}

	syncChan <- struct{}{}
}

func sendG(t *testing.T, port int, syncChan chan struct{}) {
	<-syncChan
	c, err := net.Dial("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	require.NoError(t, err)

	<-syncChan
	require.NoError(t, c.Close())
}

// Test that IORING_OP_ACCEPT return remote sockaddr.
func TestAcceptAddr(t *testing.T) {
	ring, err := New(64)
	require.NoError(t, err)
	defer ring.Close()

	tcpListener, listenerFd, err := makeTCPListener("0.0.0.0:8080")
	require.NoError(t, err)
	defer tcpListener.Close()

	clientConnChan := make(chan net.Conn)
	go dial(t, clientConnChan)

	op := Accept(listenerFd, 0)

	err = ring.QueueSQE(op, 0, 0)
	require.NoError(t, err)
	_, err = ring.Submit()
	require.NoError(t, err)

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)

	ring.SeenCQE(cqe)

	c := <-clientConnChan
	defer c.Close()

	rAddr, err := op.Addr()
	require.NoError(t, err)
	require.Equal(t, c.LocalAddr().String(), rAddr.String())
	require.Equal(t, c.LocalAddr().Network(), rAddr.Network())
}
