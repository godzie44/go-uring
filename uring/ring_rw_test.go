package uring

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"os"
	"testing"
	"unsafe"
)

const readFileName = "../go.mod"

func TestSingleReadV(t *testing.T) {
	r, err := NewRing(8)
	require.NoError(t, err)
	defer r.Close()

	f, err := os.Open(readFileName)
	require.NoError(t, err)
	defer f.Close()

	cmd, err := ReadV(f, 16)
	require.NoError(t, err)
	require.NoError(t, r.FillNextSQE(cmd.fillSQE))

	_, err = r.Submit()
	require.NoError(t, err)

	_, err = r.WaitCQEvents(1)
	require.NoError(t, err)

	expected, err := ioutil.ReadFile(readFileName)
	require.NoError(t, err)

	str := string(unsafe.Slice(cmd.IOVecs[0].Base, cmd.Size))
	assert.Equal(t, string(expected), str)
}

func TestMultipleReadV(t *testing.T) {
	r, err := NewRing(8)
	require.NoError(t, err)
	defer r.Close()

	f, err := os.Open(readFileName)
	require.NoError(t, err)
	defer f.Close()

	cmd, err := ReadV(f, 16)
	require.NoError(t, err)
	require.NoError(t, r.FillNextSQE(cmd.fillSQE))

	cmd2, err := ReadV(f, 16)
	require.NoError(t, err)
	require.NoError(t, r.FillNextSQE(cmd2.fillSQE))

	_, err = r.Submit()
	require.NoError(t, err)

	_, err = r.WaitCQEvents(2)
	require.NoError(t, err)

	expected, err := ioutil.ReadFile(readFileName)
	require.NoError(t, err)

	str := string(unsafe.Slice(cmd.IOVecs[0].Base, cmd.Size))
	assert.Equal(t, string(expected), str)
	str2 := string(unsafe.Slice(cmd2.IOVecs[0].Base, cmd2.Size))
	assert.Equal(t, string(expected), str2)
}

func TestSingleWriteV(t *testing.T) {
	ring, err := NewRing(8)
	require.NoError(t, err)
	defer ring.Close()

	const testFileName = "/tmp/single_writev.txt"

	f, err := os.Create(testFileName)
	require.NoError(t, err)
	defer os.Remove(testFileName)
	defer f.Close()

	writeData := [][]byte{
		[]byte("writev test line 1 \n"),
		[]byte("writev test line 2 \n"),
		[]byte("writev test line 3 \n"),
	}

	require.NoError(t, ring.FillNextSQE(WriteV(f, writeData, 0).fillSQE))

	_, err = ring.Submit()
	require.NoError(t, err)

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)
	require.Equal(t, len(writeData[0])+len(writeData[1])+len(writeData[2]), int(cqe.Res))

	recorded, err := ioutil.ReadFile(testFileName)
	require.NoError(t, err)

	require.Equal(t, "writev test line 1 \nwritev test line 2 \nwritev test line 3 \n", string(recorded))
}
