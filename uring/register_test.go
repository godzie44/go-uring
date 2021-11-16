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
)

//TestProbe test IORING_REGISTER_PROBE
func TestProbe(t *testing.T) {
	ring, err := New(4)
	require.NoError(t, err)
	defer ring.Close()

	probe, err := ring.Probe()
	if errors.Is(err, syscall.EINVAL) {
		t.Skip("Skipped, IORING_REGISTER_PROBE not supported")
	}
	require.NoError(t, err)

	assert.NotEqual(t, 0, probe.lastOp)
	assert.NotEqual(t, 0, probe.ops)

	assert.NotEqual(t, 0, probe.GetOP(int(NopCode)).Flags&OpSupportedFlag, "NOP not supported")
	assert.NotEqual(t, 0, probe.GetOP(int(ReadVCode)).Flags&OpSupportedFlag, "READV not supported")
	assert.NotEqual(t, 0, probe.GetOP(int(WriteVCode)).Flags&OpSupportedFlag, "WRITEV not supported")
}

//TestIOWQMaxWorkers test IORING_REGISTER_IOWQ_MAX_WORKERS
func TestIOWQMaxWorkers(t *testing.T) {
	ring, err := New(4)
	require.NoError(t, err)
	defer ring.Close()

	err = ring.SetIOWQMaxWorkers(runtime.NumCPU())
	if errors.Is(err, syscall.EINVAL) {
		t.Skip("Skipped, IORING_REGISTER_IOWQ_MAX_WORKERS not supported")
	}
	require.NoError(t, err)
}
