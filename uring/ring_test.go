//go:build linux

package uring

import (
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"syscall"
	"testing"
)

func TestCreateRing(t *testing.T) {
	r, err := New(64)
	require.NoError(t, err)

	assert.NotEqual(t, 0, r.Fd())

	err = r.Close()
	require.NoError(t, err)
}

func TestCreateManyRings(t *testing.T) {
	type testCase struct {
		ringCnt               int
		workerPoolCnt         int
		expectedAppendedWQCnt int
	}
	testCases := []testCase{
		{ringCnt: 4, workerPoolCnt: 4, expectedAppendedWQCnt: 0},
		{ringCnt: 4, workerPoolCnt: 2, expectedAppendedWQCnt: 2},
		{ringCnt: 4, workerPoolCnt: 1, expectedAppendedWQCnt: 3},
	}

	for _, tc := range testCases {
		rings, closeFn, err := CreateMany(tc.ringCnt, 64, tc.workerPoolCnt)
		require.NoError(t, err)

		assert.Len(t, rings, tc.ringCnt)
		for _, r := range rings {
			assert.NotEqual(t, 0, r.Fd())
		}

		var appendedWQs int
		for _, r := range rings {
			if r.Params.wqFD != 0 {
				appendedWQs++
			}
		}
		assert.Equal(t, appendedWQs, tc.expectedAppendedWQCnt)

		err = closeFn()
		require.NoError(t, err)

		for _, r := range rings {
			err = syscall.Close(r.Fd())
			assert.ErrorIs(t, err, syscall.EBADF)
		}
	}
}

func queueNOPs(r *Ring, count int, offset int) (err error) {
	for i := 0; i < count; i++ {
		err = r.QueueSQE(Nop(), 0, uint64(i+offset))
		if err != nil {
			return err
		}
	}
	_, err = r.Submit()
	return err
}

//TestCQRingReady test CQ ready.
func TestCQRingReady(t *testing.T) {
	ring, err := New(4)
	require.NoError(t, err)
	defer ring.Close()

	assert.Equal(t, uint32(0), ring.cqRing.readyCount())

	require.NoError(t, queueNOPs(ring, 4, 0))
	assert.Equal(t, uint32(4), ring.cqRing.readyCount())
	ring.AdvanceCQ(4)

	assert.Equal(t, uint32(0), ring.cqRing.readyCount())

	require.NoError(t, queueNOPs(ring, 4, 0))
	assert.Equal(t, uint32(4), ring.cqRing.readyCount())

	ring.AdvanceCQ(1)

	assert.Equal(t, uint32(3), ring.cqRing.readyCount())

	ring.AdvanceCQ(2)

	assert.Equal(t, uint32(1), ring.cqRing.readyCount())

	ring.AdvanceCQ(1)

	assert.Equal(t, uint32(0), ring.cqRing.readyCount())
}

//TestCQRingFull test cq ring overflow.
func TestCQRingFull(t *testing.T) {
	ring, err := New(4)
	require.NoError(t, err)
	defer ring.Close()

	require.NoError(t, queueNOPs(ring, 4, 0))
	require.NoError(t, queueNOPs(ring, 4, 0))
	require.NoError(t, queueNOPs(ring, 4, 0))

	i := 0
	for {
		_, cqe, err := ring.peekCQEvent()
		if err != nil && errors.Is(err, syscall.EAGAIN) {
			break
		}
		if err != nil {
			assert.Fail(t, "wait completion", err)
		}
		ring.SeenCQE(cqe)
		if cqe == nil {
			break
		}
		i++
	}

	assert.GreaterOrEqual(t, i, 8)
	assert.False(t, *ring.cqRing.kOverflow != 4 && !(ring.Params.NoDropFeature()))
}

//TestCQRingSize test CQ ring sizing.
func TestCQRingSize(t *testing.T) {
	ring, err := New(4, WithCQSize(64))
	if errors.Is(err, syscall.EINVAL) {
		t.Skip("Skipped, not supported on this kernel")
		return
	}
	require.NoError(t, err)

	assert.GreaterOrEqual(t, ring.Params.cqEntries, uint32(64))
	require.NoError(t, ring.Close())

	_, err = New(4, WithCQSize(0))
	assert.Error(t, err, "zero sized cq ring succeeded")
}

func fillNOPs(r *Ring) (filled int) {
	for {
		if err := r.QueueSQE(Nop(), 0, 0); errors.Is(err, ErrSQOverflow) {
			break
		}
		filled++
	}
	return filled
}

//TestRingNopAllSizes exercise full filling of SQ and CQ ring.
func TestRingNopAllSizes(t *testing.T) {
	var depth uint32 = 1
	for depth <= MaxEntries {
		ring, err := New(depth)
		if errors.Is(err, syscall.ENOMEM) {
			t.Skip("Skipped, not enough memory:", depth, "entries")
			return
		}
		require.NoError(t, err)

		var total uint
		fillNOPs(ring)
		ret, err := ring.Submit()
		require.NoError(t, err)
		total += ret

		fillNOPs(ring)
		ret, err = ring.Submit()
		require.NoError(t, err)
		total += ret

		for i := 0; i < int(total); i++ {
			cqe, err := ring.WaitCQEvents(1)
			require.NoError(t, err)
			ring.SeenCQE(cqe)
		}

		require.NoError(t, ring.Close())
		depth <<= 1
	}
}

//TestCQPeekBatch test CQ peek-batch.
func TestCQPeekBatch(t *testing.T) {
	ring, err := New(4)
	require.NoError(t, err)
	defer ring.Close()

	cqeBuff := make([]*CQEvent, 128)

	cnt := ring.PeekCQEventBatch(cqeBuff)
	assert.Equal(t, 0, cnt)

	require.NoError(t, queueNOPs(ring, 4, 0))

	cnt = ring.PeekCQEventBatch(cqeBuff)
	assert.Equal(t, 4, cnt)
	for i := 0; i < 4; i++ {
		assert.Equal(t, uint64(i), cqeBuff[i].UserData)
	}

	require.NoError(t, queueNOPs(ring, 4, 4))

	ring.AdvanceCQ(4)
	cnt = ring.PeekCQEventBatch(cqeBuff)
	assert.Equal(t, 4, cnt)
	for i := 0; i < 4; i++ {
		assert.Equal(t, uint64(i+4), cqeBuff[i].UserData)
	}

	ring.AdvanceCQ(4)
}
