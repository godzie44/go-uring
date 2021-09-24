package uring

import (
	"math"
	"os"
	"syscall"
	"time"
	"unsafe"
)

type opcode uint8

const (
	opNop opcode = iota
	opReadV
	opWriteV
	opFSync
	opReadFixed
	opWriteFixed
	opPollAdd
	opPollRemove
	opSyncFileRange
	opSendMsg
	opRecvMsg
	opTimeout
	opTimeoutRemove
	opAccept
)

type baseCommand struct {
	userData uint64
}

func (cmd *baseCommand) SetUserData(v uint64) {
	cmd.userData = v
}

func (cmd *baseCommand) UserData() uint64 {
	return cmd.userData
}

//NopCommand - do not perform any I/O. This is useful for testing the performance of the io_uring implementation itself.
type NopCommand struct {
	baseCommand
}

func Nop() *NopCommand {
	return &NopCommand{}
}

func (n *NopCommand) fillSQE(sqe *SQEntry) {
	sqe.fill(opNop, -1, uintptr(unsafe.Pointer(nil)), 0, 0)
	sqe.setUserData(n.userData)
}

//ReadVCommand vectored read operation, similar to preadv2(2).
type ReadVCommand struct {
	baseCommand
	Name   string
	FD     uintptr
	Size   int64
	IOVecs []syscall.Iovec
}

//ReadV vectored read operation, similar to preadv2(2).
func ReadV(file *os.File, blockSize int64) (*ReadVCommand, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	bytesRemaining := stat.Size()
	blocks := int(math.Ceil(float64(bytesRemaining) / float64(blockSize)))

	buff := make([]byte, bytesRemaining)
	var idx int64

	buffs := make([]syscall.Iovec, 0, blocks)
	for bytesRemaining != 0 {
		bytesToRead := bytesRemaining
		if bytesToRead > blockSize {
			bytesToRead = blockSize
		}

		buffs = append(buffs, syscall.Iovec{
			Base: &buff[idx],
			Len:  uint64(bytesToRead),
		})

		idx += bytesToRead
		bytesRemaining -= bytesToRead
	}

	return &ReadVCommand{Name: file.Name(), FD: file.Fd(), Size: stat.Size(), IOVecs: buffs}, nil
}

func (cmd *ReadVCommand) fillSQE(sqe *SQEntry) {
	sqe.fill(opReadV, int32(cmd.FD), uintptr(unsafe.Pointer(&cmd.IOVecs[0])), uint32(len(cmd.IOVecs)), 0)
	sqe.setUserData(cmd.userData)
}

//WriteVCommand vectored write operation, similar to pwritev2(2).
type WriteVCommand struct {
	baseCommand
	Name   string
	FD     uintptr
	IOVecs []syscall.Iovec
	Offset uint64
}

//WriteV vectored writes bytes to file. Write starts from offset.
//If the file is not seekable, offset must be set to zero.
func WriteV(file *os.File, bytes [][]byte, offset uint64) *WriteVCommand {
	buffs := make([]syscall.Iovec, len(bytes))
	for i := range bytes {
		buffs[i].SetLen(len(bytes[i]))
		buffs[i].Base = &bytes[i][0]
	}

	return &WriteVCommand{Name: file.Name(), FD: file.Fd(), IOVecs: buffs, Offset: offset}
}

func (cmd *WriteVCommand) fillSQE(sqe *SQEntry) {
	sqe.fill(opWriteV, int32(cmd.FD), uintptr(unsafe.Pointer(&cmd.IOVecs[0])), uint32(len(cmd.IOVecs)), cmd.Offset)
	sqe.setUserData(cmd.userData)
}

//TimeoutCommand timeout command.
type TimeoutCommand struct {
	baseCommand
	dur  time.Duration
	Name string
}

//Timeout - timeout operation.
func Timeout(duration time.Duration) *TimeoutCommand {
	return &TimeoutCommand{
		dur: duration,
	}
}

func (cmd *TimeoutCommand) fillSQE(sqe *SQEntry) {
	spec := syscall.NsecToTimespec(cmd.dur.Nanoseconds())
	sqe.fill(opTimeout, -1, uintptr(unsafe.Pointer(&spec)), 1, 0)
	sqe.setUserData(cmd.userData)
}

//AcceptCommand accept command.
type AcceptCommand struct {
	baseCommand
	fd    int
	flags uint32
}

//Accept - accept operation.
func Accept(fd int, flags uint32) *AcceptCommand {
	return &AcceptCommand{
		fd:    fd,
		flags: flags,
	}
}

func (cmd *AcceptCommand) fillSQE(sqe *SQEntry) {
	sqe.fill(opAccept, int32(cmd.fd), 0, 0, 0)
	sqe.opcodeFlags = cmd.flags
}
