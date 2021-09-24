package uring

import (
	"github.com/stretchr/testify/assert"
	"syscall"
	"testing"
)

func TestCreateRing(t *testing.T) {
	r, err := NewRing(64)
	assert.NoError(t, err)

	err = r.Close()
	assert.NoError(t, err)
}

func queueNOPs(r *URing, count int, offset int) (err error) {
	for i := 0; i < count; i++ {
		nop := Nop()
		nop.SetUserData(uint64(i + offset))
		err = r.FillNextSQE(nop.fillSQE)
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

	assert.Equal(t, uint32(0), ring.cqRing.readyCount())

	assert.NoError(t, queueNOPs(ring, 4, 0))
	assert.Equal(t, uint32(4), ring.cqRing.readyCount())
	ring.AdvanceCQ(4)

	assert.Equal(t, uint32(0), ring.cqRing.readyCount())

	assert.NoError(t, queueNOPs(ring, 4, 0))
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
	ring, err := NewRing(4)
	assert.NoError(t, err)
	defer ring.Close()

	assert.NoError(t, queueNOPs(ring, 4, 0))
	assert.NoError(t, queueNOPs(ring, 4, 0))
	assert.NoError(t, queueNOPs(ring, 4, 0))

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

//TestRingProbe test IORING_REGISTER_PROBE
func TestRingProbe(t *testing.T) {
	ring, err := NewRing(4)
	assert.NoError(t, err)
	defer ring.Close()

	probe, err := ring.Probe()
	if err == syscall.EINVAL {
		t.Skip("Skipped, probe not supported")
	}
	assert.NoError(t, err)

	assert.NotEqual(t, 0, probe.lastOp)
	assert.NotEqual(t, 0, probe.ops)

	assert.NotEqual(t, 0, probe.GetOP(int(opNop)).Flags&uint16(opSupported), "NOP not supported")
	assert.NotEqual(t, 0, probe.GetOP(int(opReadV)).Flags&uint16(opSupported), "READV not supported")
	assert.NotEqual(t, 0, probe.GetOP(int(opWriteV)).Flags&uint16(opSupported), "WRITEV not supported")
}

//TestCQPeekBatch test CQ peek-batch.
func TestCQPeekBatch(t *testing.T) {
	ring, err := NewRing(4)
	assert.NoError(t, err)
	defer ring.Close()

	CQEs := ring.PeekCQEventBatch(4)
	assert.Len(t, CQEs, 0)

	assert.NoError(t, queueNOPs(ring, 4, 0))

	CQEs = ring.PeekCQEventBatch(4)
	assert.Len(t, CQEs, 4)
	for i := 0; i < 4; i++ {
		assert.Equal(t, uint64(i), CQEs[i].UserData)
	}

	assert.NoError(t, queueNOPs(ring, 4, 4))

	ring.AdvanceCQ(4)
	CQEs = ring.PeekCQEventBatch(4)
	assert.Len(t, CQEs, 4)
	for i := 0; i < 4; i++ {
		assert.Equal(t, uint64(i+4), CQEs[i].UserData)
	}

	ring.AdvanceCQ(4)
}
