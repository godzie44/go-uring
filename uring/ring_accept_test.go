package uring

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"syscall"
	"testing"
	"time"
)

func dial(t *testing.T, connChan chan<- net.Conn) {
	c, err := net.Dial("tcp", "0.0.0.0:8080")
	require.NoError(t, err)

	connChan <- c
}

func getBlockingFd(t *testing.T, conn syscall.Conn) (fdescr uintptr) {
	raw, err := conn.SyscallConn()
	require.NoError(t, err)
	err = raw.Control(func(fd uintptr) {
		require.NoError(t, syscall.SetNonblock(int(fd), false))
		fdescr = fd
	})
	require.NoError(t, err)

	return fdescr
}

const sendData = "hello world"

// Test that IORING_OP_ACCEPT works.
func TestAccept(t *testing.T) {
	ring, err := NewRing(64)
	require.NoError(t, err)
	defer ring.Close()

	tcpListener, err := net.ListenTCP("tcp", &net.TCPAddr{Port: 8080})
	require.NoError(t, err)
	defer tcpListener.Close()
	listenerFd := getBlockingFd(t, tcpListener)

	clientConnChan := make(chan net.Conn)
	go dial(t, clientConnChan)

	connFd, err := acceptConnection(t, ring, listenerFd)
	require.NoError(t, err)

	clientConn := <-clientConnChan
	require.NoError(t, err)

	require.NoError(t, queueSend(ring, getBlockingFd(t, clientConn.(*net.TCPConn)), []byte(sendData)))

	readBuff := make([]byte, 100)
	require.NoError(t, queueRecv(ring, connFd, readBuff))

	_, err = ring.SubmitAndWaitCQEvents(2)
	require.NoError(t, err)

	events := ring.PeekCQEventBatch(2)
	require.Len(t, events, 2)
	for _, cqe := range events {
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
	cmd := Accept(fd, 0)
	err = ring.FillNextSQE(cmd.fillSQE)
	require.NoError(t, err)

	_, err = ring.Submit()
	require.NoError(t, err)

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)

	ring.SeenCQE(cqe)

	return uintptr(cqe.Res), cqe.Error()
}

func queueSend(ring *URing, fd uintptr, buff []byte) error {
	cmd := &WriteVCommand{FD: fd, IOVecs: []syscall.Iovec{
		{
			Base: &buff[0],
			Len:  uint64(len(buff)),
		},
	}, Offset: 0}
	cmd.SetUserData(1)
	return ring.FillNextSQE(cmd.fillSQE)
}

func queueRecv(ring *URing, fd uintptr, buff []byte) error {
	cmd := &ReadVCommand{FD: fd, IOVecs: []syscall.Iovec{
		{
			Base: &buff[0],
			Len:  uint64(len(buff)),
		},
	}}
	cmd.SetUserData(2)
	return ring.FillNextSQE(cmd.fillSQE)
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
		ring, err := NewRing(32)
		require.NoError(t, err)

		tcpListener, err := net.ListenTCP("tcp", &net.TCPAddr{Port: 8080})
		require.NoError(t, err)

		acceptCmd := Accept(getBlockingFd(t, tcpListener), 0)
		acceptCmd.SetUserData(1)
		require.NoError(t, ring.FillNextSQE(acceptCmd.fillSQE))

		_, err = ring.Submit()
		require.NoError(t, err)

		if tc.cancelDelay != 0 {
			time.Sleep(tc.cancelDelay)
		}

		cancelCmd := Cancel(1, 0)
		cancelCmd.SetUserData(2)
		require.NoError(t, ring.FillNextSQE(cancelCmd.fillSQE))
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
		count int
		delay time.Duration
	}
	testCases := []testCase{
		{1, 0},
		{1, time.Microsecond * 10000},
	}

	for n, tc := range testCases {
		ring, err := NewRing(uint32(2 * tc.count))
		require.NoError(t, err)

		listeners := make([]*net.TCPListener, 0, tc.count)
		for i := 0; i < tc.count; i++ {
			tcpListener, err := net.ListenTCP("tcp", &net.TCPAddr{Port: 8080 + (n * tc.count) + i})
			require.NoError(t, err)
			listeners = append(listeners, tcpListener)

			listenerFd := getBlockingFd(t, tcpListener)
			cmd := Accept(listenerFd, 0)
			err = ring.FillNextSQE(cmd.fillSQE)
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
