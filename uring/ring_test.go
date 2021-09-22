package uring

import (
	"github.com/stretchr/testify/assert"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestCreateRing(t *testing.T) {
	r, err := NewRing(64)
	assert.NoError(t, err)

	err = r.Close()
	assert.NoError(t, err)
}

func readyCnt(r *URing) uint32 {
	return atomic.LoadUint32(r.cqRing.kTail) - atomic.LoadUint32(r.cqRing.kHead)
}

func queueNOPs(r *URing, count int) (err error) {
	for i := 0; i < count; i++ {
		err = r.FillNextSQE(Nop().fillSQE)
		if err != nil {
			return err
		}
	}
	_, err = r.Submit()
	return err
}

//TestCQRingReady test CQ ready.
func TestCQRingReady(t *testing.T) {
	ring, err := NewRing(4)
	assert.NoError(t, err)
	defer ring.Close()

	assert.Equal(t, uint32(0), readyCnt(ring))

	assert.NoError(t, queueNOPs(ring, 4))
	assert.Equal(t, uint32(4), readyCnt(ring))
	ring.AdvanceCQ(4)

	assert.Equal(t, uint32(0), readyCnt(ring))

	assert.NoError(t, queueNOPs(ring, 4))
	assert.Equal(t, uint32(4), readyCnt(ring))

	ring.AdvanceCQ(1)

	assert.Equal(t, uint32(3), readyCnt(ring))

	ring.AdvanceCQ(2)

	assert.Equal(t, uint32(1), readyCnt(ring))

	ring.AdvanceCQ(1)

	assert.Equal(t, uint32(0), readyCnt(ring))
}

//TestCQRingFull test cq ring overflow.
func TestCQRingFull(t *testing.T) {
	ring, err := NewRing(4)
	assert.NoError(t, err)
	defer ring.Close()

	assert.NoError(t, queueNOPs(ring, 4))
	assert.NoError(t, queueNOPs(ring, 4))
	assert.NoError(t, queueNOPs(ring, 4))

	i := 0
	for {
		_, cqe, err := ring.peekCQEvent()
		if err != nil && err == syscall.EAGAIN {
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
	assert.False(t, *ring.cqRing.kOverflow != 4 && !(ring.params.FeatNoDrop()))
}

//TestCQRingSize test CQ ring sizing.
func TestCQRingSize(t *testing.T) {
	ring, err := NewRing(4, WithCQSize(64))
	if err == syscall.EINVAL {
		t.Skip("Skipped, not supported on this kernel")
		return
	}
	assert.NoError(t, err)

	assert.GreaterOrEqual(t, ring.params.cqEntries, uint32(64))
	assert.NoError(t, ring.Close())

	ring, err = NewRing(4, WithCQSize(0))
	assert.Error(t, err, "zero sized cq ring succeeded")
	assert.NoError(t, ring.Close())
}

// static int fill_nops(struct io_uring *ring)
//{
//	struct io_uring_sqe *sqe;
//	int filled = 0;
//
//	do {
//		sqe = io_uring_get_sqe(ring);
//		if (!sqe)
//			break;
//
//		io_uring_prep_nop(sqe);
//		filled++;
//	} while (1);
//
//	return filled;
//}

func fillNOPs(r *URing) (filled int) {
	for {
		sqe, err := r.NextSQE()
		if err == ErrSQRingOverflow {
			break
		}
		Nop().fillSQE(sqe)
		filled++
	}
	return filled
}

//TestRingNopAllSizes exercise full filling of SQ and CQ ring.
func TestRingNopAllSizes(t *testing.T) {
	var depth uint32 = 1
	for depth <= MaxEntries {
		ring, err := NewRing(depth)
		if err == syscall.ENOMEM {
			t.Skip("Skipped, not enough memory")
			return
		}
		assert.NoError(t, err)

		var total uint
		fillNOPs(ring)
		ret, err := ring.Submit()
		assert.NoError(t, err)
		total += ret

		fillNOPs(ring)
		ret, err = ring.Submit()
		assert.NoError(t, err)
		total += ret

		for i := 0; i < int(total); i++ {
			cqe, err := ring.WaitCQEvents(1)
			assert.NoError(t, err)
			ring.SeenCQE(cqe)
		}

		assert.NoError(t, ring.Close())
		depth <<= 1
	}
}
