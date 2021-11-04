package uring

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"sync"
	"syscall"
	"testing"
)

var str = "This is a test of sendmsg and recvmsg over io_uring!"

func TestSendRecv(t *testing.T) {
	mu := &sync.Mutex{}
	mu.Lock()
	cond := sync.NewCond(mu)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		recv(t, cond)
	}()
	cond.Wait()

	send(t)

	wg.Wait()
}

func send(t *testing.T) {
	ring, err := New(1)
	require.NoError(t, err)
	defer ring.Close()

	conn, err := net.Dial("udp", "127.0.0.1:8080")
	require.NoError(t, err)
	defer conn.Close()

	f, err := conn.(*net.UDPConn).File()
	require.NoError(t, err)

	vec := syscall.Iovec{
		Base: &[]byte(str)[0],
		Len:  uint64(len(str)),
	}

	require.NoError(t, ring.QueueSQE(Send(f.Fd(), vec, 0), 0, 1))

	_, err = ring.SubmitAndWaitCQEvents(1)
	require.NoError(t, err)
}

func recv(t *testing.T, cond *sync.Cond) {
	ring, err := New(1)
	require.NoError(t, err)
	defer ring.Close()

	pc, err := net.ListenPacket("udp", ":8080")
	require.NoError(t, err)
	defer pc.Close()

	f, err := pc.(*net.UDPConn).File()
	require.NoError(t, err)

	buff := make([]byte, 128)
	vec := syscall.Iovec{
		Base: &buff[0],
	}
	vec.SetLen(len(buff))

	require.NoError(t, ring.QueueSQE(Recv(f.Fd(), vec, 0), 0, 2))

	_, err = ring.Submit()
	require.NoError(t, err)

	cond.Signal()

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)

	if cqe.Error() == syscall.EINVAL {
		t.Skipf("Skipped, recv not supported on this kernel")
	}
	require.NoError(t, cqe.Error())

	assert.Equal(t, cqe.Res, int32(len(str)))
	assert.Equal(t, []byte(str), buff[:len(str)])
}
