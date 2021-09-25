package uring

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"runtime"
	"syscall"
	"testing"
	"time"
)

//TestSingleTimeout test single timeout command.
func TestSingleTimeout(t *testing.T) {
	r, err := NewRing(8)
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, r.FillNextSQE(Timeout(time.Second).fillSQE))

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
	r, err := NewRing(8)
	require.NoError(t, err)
	defer r.Close()

	twoSecTimeout := Timeout(2 * time.Second)
	twoSecTimeout.SetUserData(1)
	require.NoError(t, r.FillNextSQE(twoSecTimeout.fillSQE))

	oneSecTimeout := Timeout(time.Second)
	oneSecTimeout.SetUserData(2)
	require.NoError(t, r.FillNextSQE(oneSecTimeout.fillSQE))

	submitTime := time.Now()
	_, err = r.Submit()
	require.NoError(t, err)

	i := 0
ENDTEST:
	for {
		cqe, err := r.WaitCQEvents(1)
		if err == syscall.EAGAIN || err == syscall.EINTR {
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
	r, err := NewRing(8)
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, r.FillNextSQE(Nop().fillSQE))
	require.NoError(t, r.FillNextSQE(Nop().fillSQE))

	var i = 0
	for {
		cqe, err := r.WaitCQEventsWithTimeout(2, time.Second)
		if err == syscall.ETIME {
			break
		}
		if err == syscall.EINTR || err == syscall.EAGAIN {
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
