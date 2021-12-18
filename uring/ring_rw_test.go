//go:build linux

package uring

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"math"
	"os"
	"testing"
)

const readFileName = "../go.mod"

func makeVectors(file *os.File, vectorSz int64) ([][]byte, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	bytesRemaining := stat.Size()
	blocks := int(math.Ceil(float64(bytesRemaining) / float64(vectorSz)))

	buffs := make([][]byte, 0, blocks)
	for bytesRemaining != 0 {
		bytesToRead := bytesRemaining
		if bytesToRead > vectorSz {
			bytesToRead = vectorSz
		}

		buffs = append(buffs, make([]byte, bytesToRead))
		bytesRemaining -= bytesToRead
	}

	return buffs, nil
}

func vectorsToString(vectors [][]byte) string {
	var str string
	for _, v := range vectors {
		str += string(v)
	}
	return str
}

func TestSingleReadV(t *testing.T) {
	r, err := New(8)
	require.NoError(t, err)
	defer r.Close()

	f, err := os.Open(readFileName)
	require.NoError(t, err)
	defer f.Close()

	vectors, err := makeVectors(f, 16)
	require.NoError(t, err)

	require.NoError(t, r.QueueSQE(ReadV(f, vectors, 0), 0, 0))

	_, err = r.Submit()
	require.NoError(t, err)

	_, err = r.WaitCQEvents(1)
	require.NoError(t, err)

	expected, err := ioutil.ReadFile(readFileName)
	require.NoError(t, err)

	assert.Equal(t, string(expected), vectorsToString(vectors))
}

func TestMultipleReadV(t *testing.T) {
	r, err := New(8)
	require.NoError(t, err)
	defer r.Close()

	f, err := os.Open(readFileName)
	require.NoError(t, err)
	defer f.Close()

	vectors1, err := makeVectors(f, 16)
	require.NoError(t, err)
	require.NoError(t, r.QueueSQE(ReadV(f, vectors1, 0), 0, 0))

	vectors2, err := makeVectors(f, 16)
	require.NoError(t, err)
	require.NoError(t, r.QueueSQE(ReadV(f, vectors2, 0), 0, 0))

	_, err = r.Submit()
	require.NoError(t, err)

	_, err = r.WaitCQEvents(2)
	require.NoError(t, err)

	expected, err := ioutil.ReadFile(readFileName)
	require.NoError(t, err)

	assert.Equal(t, string(expected), vectorsToString(vectors1))
	assert.Equal(t, string(expected), vectorsToString(vectors2))
}

func TestSingleWriteV(t *testing.T) {
	ring, err := New(8)
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

	require.NoError(t, ring.QueueSQE(WriteV(f, writeData, 0), 0, 0))

	_, err = ring.Submit()
	require.NoError(t, err)

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)
	require.Equal(t, len(writeData[0])+len(writeData[1])+len(writeData[2]), int(cqe.Res))

	recorded, err := ioutil.ReadFile(testFileName)
	require.NoError(t, err)

	require.Equal(t, "writev test line 1 \nwritev test line 2 \nwritev test line 3 \n", string(recorded))
}

func TestRead(t *testing.T) {
	r, err := New(8)
	require.NoError(t, err)
	defer r.Close()

	f, err := os.Open(readFileName)
	require.NoError(t, err)
	defer f.Close()

	info, err := f.Stat()
	require.NoError(t, err)

	buff := make([]byte, info.Size())

	require.NoError(t, r.QueueSQE(Read(f.Fd(), buff, 0), 0, 0))

	_, err = r.Submit()
	require.NoError(t, err)

	_, err = r.WaitCQEvents(1)
	require.NoError(t, err)

	expected, err := ioutil.ReadFile(readFileName)
	require.NoError(t, err)

	assert.Equal(t, string(expected), string(buff))
}

func TestWrite(t *testing.T) {
	ring, err := New(8)
	require.NoError(t, err)
	defer ring.Close()

	const testFileName = "/tmp/single_write.txt"

	f, err := os.Create(testFileName)
	require.NoError(t, err)
	defer os.Remove(testFileName)
	defer f.Close()

	writeData := []byte("write test line 1 \nwrite test line 2 \nwrite test line 3 \n")
	require.NoError(t, ring.QueueSQE(Write(f.Fd(), writeData, 0), 0, 0))

	_, err = ring.Submit()
	require.NoError(t, err)

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)
	require.Equal(t, len(writeData), int(cqe.Res))

	recorded, err := ioutil.ReadFile(testFileName)
	require.NoError(t, err)

	require.Equal(t, "write test line 1 \nwrite test line 2 \nwrite test line 3 \n", string(recorded))
}
