package uring

import (
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"os"
	"testing"
	"unsafe"
)

const readFileName = "../go.mod"

func TestSingleReadV(t *testing.T) {
	r, err := NewRing(8)
	assert.NoError(t, err)
	defer r.Close()

	f, err := os.Open(readFileName)
	assert.NoError(t, err)
	defer f.Close()

	cmd, err := ReadV(f, 16)
	assert.NoError(t, err)
	assert.NoError(t, r.FillNextSQE(cmd.fillSQE))

	_, err = r.Submit()
	assert.NoError(t, err)

	_, err = r.WaitCQEvents(1)
	assert.NoError(t, err)

	expected, err := ioutil.ReadFile(readFileName)
	assert.NoError(t, err)

	str := string(unsafe.Slice(cmd.IOVecs[0].Base, cmd.Size))
	assert.Equal(t, string(expected), str)
}

func TestMultipleReadV(t *testing.T) {
	r, err := NewRing(8)
	assert.NoError(t, err)
	defer r.Close()

	f, err := os.Open(readFileName)
	assert.NoError(t, err)
	defer f.Close()

	cmd, err := ReadV(f, 16)
	assert.NoError(t, err)
	assert.NoError(t, r.FillNextSQE(cmd.fillSQE))

	cmd2, err := ReadV(f, 16)
	assert.NoError(t, err)
	assert.NoError(t, r.FillNextSQE(cmd2.fillSQE))

	_, err = r.Submit()
	assert.NoError(t, err)

	_, err = r.WaitCQEvents(2)
	assert.NoError(t, err)

	expected, err := ioutil.ReadFile(readFileName)
	assert.NoError(t, err)

	str := string(unsafe.Slice(cmd.IOVecs[0].Base, cmd.Size))
	assert.Equal(t, string(expected), str)
	str2 := string(unsafe.Slice(cmd2.IOVecs[0].Base, cmd2.Size))
	assert.Equal(t, string(expected), str2)
}

func TestSingleWriteV(t *testing.T) {
	ring, err := NewRing(8)
	assert.NoError(t, err)
	defer ring.Close()

	const testFileName = "/tmp/single_writev.txt"

	f, err := os.Create(testFileName)
	assert.NoError(t, err)
	defer os.Remove(testFileName)
	defer f.Close()

	writeData := [][]byte{
		[]byte("writev test line 1 \n"),
		[]byte("writev test line 2 \n"),
		[]byte("writev test line 3 \n"),
	}

	assert.NoError(t, ring.FillNextSQE(WriteV(f, writeData, 0).fillSQE))

	_, err = ring.Submit()
	assert.NoError(t, err)

	cqe, err := ring.WaitCQEvents(1)
	assert.NoError(t, err)
	assert.Equal(t, len(writeData[0])+len(writeData[1])+len(writeData[2]), int(cqe.Res))

	recorded, err := ioutil.ReadFile(testFileName)
	assert.NoError(t, err)

	assert.Equal(t, "writev test line 1 \nwritev test line 2 \nwritev test line 3 \n", string(recorded))
}
