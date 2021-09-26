package uring

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"syscall"
	"testing"
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
