package uring

import (
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"syscall"
	"testing"
)

func TestConnect(t *testing.T) {
	ring, err := New(64)
	require.NoError(t, err)
	defer ring.Close()

	const addr = "0.0.0.0:8088"
	stopServer := startServer(t, addr)
	defer stopServer()

	socketFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	defer syscall.Close(socketFd)

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	require.NoError(t, err)

	err = ring.QueueSQE(Connect(uintptr(socketFd), tcpAddr), 0, 0)
	require.NoError(t, err)
	_, err = ring.Submit()
	require.NoError(t, err)

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)

	ring.SeenCQE(cqe)

	require.NoError(t, cqe.Error())
	require.Equal(t, int32(0), cqe.Res)
}

func startServer(t *testing.T, addr string) func() {
	l, err := net.Listen("tcp", addr)
	require.NoError(t, err)

	go func() {
		_ = http.Serve(l, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		}))
	}()

	return func() {
		l.Close()
	}
}
