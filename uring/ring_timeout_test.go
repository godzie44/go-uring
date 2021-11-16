//go:build linux
// +build linux

package uring

import (
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"runtime"
	"syscall"
	"testing"
	"time"
)

//TestSingleTimeout test single timeout command.
func TestSingleTimeout(t *testing.T) {
	r, err := New(8)
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, r.QueueSQE(Timeout(time.Second), 0, 0))

	submitTime := time.Now()
	_, err = r.Submit()
	require.NoError(t, err)

	cqe, err := r.WaitCQEvents(1)
	require.NoError(t, err)
	assert.Equal(t, syscall.ETIME, cqe.Error())

	assert.True(t, time.Since(submitTime) > time.Second)
}

//TestMultipleTimeout test multiple timeouts command.
func TestMultipleTimeout(t *testing.T) {
	r, err := New(8)
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, r.QueueSQE(Timeout(2*time.Second), 0, 1))
	require.NoError(t, r.QueueSQE(Timeout(time.Second), 0, 2))

	submitTime := time.Now()
	_, err = r.Submit()
	require.NoError(t, err)

	i := 0
ENDTEST:
	for {
		cqe, err := r.WaitCQEvents(1)
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			runtime.Gosched()
			continue
		}

		require.NoError(t, err)

		switch i {
		case 0:
			assert.Equal(t, uint64(2), cqe.UserData)
			assert.True(t, time.Since(submitTime) > time.Second && time.Since(submitTime) < 2*time.Second)
			r.SeenCQE(cqe)
		case 1:
			assert.Equal(t, uint64(1), cqe.UserData)
			assert.True(t, time.Since(submitTime) > 2*time.Second)
			r.SeenCQE(cqe)
			break ENDTEST
		}
		i++
	}
}

//TestSingleTimeoutWait test wait cq event with timeout function.
func TestSingleTimeoutWait(t *testing.T) {
	r, err := New(8)
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, r.QueueSQE(Nop(), 0, 1))
	require.NoError(t, r.QueueSQE(Nop(), 0, 2))

	if r.Params.ExtArgFeature() {
		c, err := r.Submit()
		require.NoError(t, err)
		require.Equal(t, 2, int(c))
	}

	var i = 0
	for {
		cqe, err := r.WaitCQEventsWithTimeout(2, time.Second*2)
		if errors.Is(err, syscall.ETIME) {
			break
		}

		if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
			runtime.Gosched()
			continue
		}

		require.NoError(t, err)
		r.SeenCQE(cqe)

		require.NoError(t, cqe.Error())
		i++
	}

	assert.Equal(t, 2, i)
}
