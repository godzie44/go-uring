//go:build linux

package uring

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"math/rand"
	"os"
	"syscall"
	"testing"
)

const fileSize = 128 * 1024
const bufferSize = 4096
const bufferCnt = fileSize / bufferSize

//TestIOPoll basic read/write tests with polled IO.
func TestIOPoll(t *testing.T) {
	fName := fmt.Sprintf("./tests/tmp/basic-rw-%d-%d", rand.Uint32(), syscall.Getpid())
	require.NoError(t, os.WriteFile(fName, bytes.Repeat([]byte("io"), fileSize/2), 0644))
	defer os.Remove(fName)

	type testCase struct {
		write, buffSelect bool
	}
	var testCases = []testCase{
		{write: false, buffSelect: false},
		{write: true, buffSelect: false},
	}

	if hasBuffSelect(t) {
		testCases = append(testCases, testCase{
			write:      false,
			buffSelect: true,
		})
	}

	vectors := createBuffers(bufferCnt, bufferSize)

	for _, tc := range testCases {
		testIO(t, fName, vectors, tc.write, tc.buffSelect)
	}
}

func hasBuffSelect(t *testing.T) bool {
	ring, err := New(1, WithIOPoll())
	if err != nil {
		t.Skipf("IOPOLL test skipped, reason: %s", err.Error())
	}
	defer ring.Close()

	probe, err := ring.Probe()
	require.NoError(t, err)

	return probe.GetOP(int(ProvideBuffersCode)).Flags&OpSupportedFlag != 0
}

func createBuffers(cnt int, size int) [][]byte {
	result := make([][]byte, cnt)
	for i := 0; i < cnt; i++ {
		result[i] = make([]byte, size)
	}
	return result
}

func provideBuffers(ring *Ring, vectors [][]byte) error {
	for i, v := range vectors {
		op := ProvideBuffers(v, uint64(i), 1)
		if err := ring.QueueSQE(op, 0, 0); err != nil {
			return err
		}
	}

	res, err := ring.Submit()
	if err != nil {
		return err
	}
	if int(res) != len(vectors) {
		return errors.New("invalid submit count")
	}

	for i := 0; i < len(vectors); i++ {
		cqe, err := ring.WaitCQEvents(1)
		if err != nil {
			return err
		}
		if cqe.Error() != nil {
			return err
		}
		ring.SeenCQE(cqe)
	}

	return nil
}

func testIO(t *testing.T, fName string, vectors [][]byte, write, bufSelect bool) {
	ring, err := New(64, WithIOPoll())
	require.NoError(t, err)
	defer ring.Close()

	if bufSelect {
		write = false
	}

	if bufSelect {
		assert.NoError(t, provideBuffers(ring, vectors))
	}

	var flags int
	if write {
		flags |= syscall.O_WRONLY
	} else {
		flags |= syscall.O_RDONLY
	}
	file, err := os.OpenFile(fName, flags|syscall.O_DIRECT, 0644)
	require.NoError(t, err)
	defer file.Close()

	for i := 0; i < bufferCnt; i++ {
		offset := bufferSize * (rand.Int() % bufferCnt)
		if write {
			op := WriteV(file, [][]byte{vectors[i]}, uint64(offset))
			err := ring.QueueSQE(op, 0, uint64(i))
			require.NoError(t, err)
		} else {
			op := ReadV(file, [][]byte{vectors[i]}, uint64(offset))
			if bufSelect {
				sqe, err := ring.NextSQE()
				require.NoError(t, err)
				op.PrepSQE(sqe)
				sqe.Flags = SqeBufferSelectFlag
				sqe.setUserData(uint64(i))
				sqe.BufIG = 1
			} else {
				err := ring.QueueSQE(op, 0, uint64(i))
				require.NoError(t, err)
			}
		}
	}

	submitted, err := ring.Submit()
	require.NoError(t, err)
	require.Equal(t, bufferCnt, int(submitted))

	for i := 0; i < bufferCnt; i++ {
		cqe, err := ring.WaitCQEvents(1)
		require.NoError(t, err)

		if cqe.Error() != nil && errors.Is(cqe.Error(), syscall.EOPNOTSUPP) {
			t.Skipf("File/device/fs doesn't support polled IO")
		}

		assert.NoError(t, cqe.Error())
		assert.Equal(t, bufferSize, int(cqe.Res))

		ring.SeenCQE(cqe)
	}
}
