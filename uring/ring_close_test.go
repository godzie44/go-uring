package uring

import (
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"os"
	"testing"
)

//TestRingClose test Close operation.
func TestRingClose(t *testing.T) {
	r, err := New(2)
	require.NoError(t, err)
	defer r.Close()

	f, err := os.Open("./../go.mod")
	require.NoError(t, err)

	err = r.QueueSQE(Close(f.Fd()), 0, 0)
	require.NoError(t, err)

	_, err = r.Submit()
	require.NoError(t, err)

	cqe, err := r.WaitCQEvents(1)
	require.NoError(t, err)

	require.NoError(t, cqe.Error())

	_, err = unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
	require.ErrorIs(t, err, unix.EBADF)
}
