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
	opAsyncCancel
	opLinkTimeout
	opConnect
	opFAllocate
	opOpenAt
	opClose
	opFilesUpdate
	opStatX
	opRead
	opWrite
	opFAdvise
	opMAdvise
	opSend
	opRecv
)

//NopOp - do not perform any I/O. This is useful for testing the performance of the io_uring implementation itself.
type NopOp struct {
}

func Nop() *NopOp {
	return &NopOp{}
}

func (op *NopOp) PrepSQE(sqe *SQEntry) {
	sqe.fill(opNop, -1, uintptr(unsafe.Pointer(nil)), 0, 0)
}

//ReadVOp vectored read operation, similar to preadv2(2).
type ReadVOp struct {
	FD     uintptr
	Size   int64
	IOVecs []syscall.Iovec
}

//ReadV vectored read operation, similar to preadv2(2).
func ReadV(file *os.File, blockSize int64) (*ReadVOp, error) {
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

	return &ReadVOp{FD: file.Fd(), Size: stat.Size(), IOVecs: buffs}, nil
}

func (op *ReadVOp) PrepSQE(sqe *SQEntry) {
	sqe.fill(opReadV, int32(op.FD), uintptr(unsafe.Pointer(&op.IOVecs[0])), uint32(len(op.IOVecs)), 0)
}

//WriteVOp vectored write operation, similar to pwritev2(2).
type WriteVOp struct {
	FD     uintptr
	IOVecs []syscall.Iovec
	Offset uint64
}

//WriteV vectored writes bytes to file. Write starts from offset.
//If the file is not seekable, offset must be set to zero.
func WriteV(file *os.File, bytes [][]byte, offset uint64) *WriteVOp {
	buffs := make([]syscall.Iovec, len(bytes))
	for i := range bytes {
		buffs[i].SetLen(len(bytes[i]))
		buffs[i].Base = &bytes[i][0]
	}

	return &WriteVOp{FD: file.Fd(), IOVecs: buffs, Offset: offset}
}

func (op *WriteVOp) PrepSQE(sqe *SQEntry) {
	sqe.fill(opWriteV, int32(op.FD), uintptr(unsafe.Pointer(&op.IOVecs[0])), uint32(len(op.IOVecs)), op.Offset)
}

//TimeoutOp timeout command.
type TimeoutOp struct {
	dur time.Duration
}

//Timeout - timeout operation.
func Timeout(duration time.Duration) *TimeoutOp {
	return &TimeoutOp{
		dur: duration,
	}
}

func (op *TimeoutOp) PrepSQE(sqe *SQEntry) {
	spec := syscall.NsecToTimespec(op.dur.Nanoseconds())
	sqe.fill(opTimeout, -1, uintptr(unsafe.Pointer(&spec)), 1, 0)
}

//AcceptOp accept command.
type AcceptOp struct {
	fd    uintptr
	flags uint32
}

//Accept - accept operation.
func Accept(fd uintptr, flags uint32) *AcceptOp {
	return &AcceptOp{
		fd:    fd,
		flags: flags,
	}
}

func (op *AcceptOp) PrepSQE(sqe *SQEntry) {
	sqe.fill(opAccept, int32(op.fd), 0, 0, 0)
	sqe.OpcodeFlags = op.flags
}

//CancelOp Attempt  to cancel an already issued request.
type CancelOp struct {
	flags          uint32
	targetUserData uint64
}

//Cancel create CancelOp. Put in targetUserData value of user_data field of the request that should be cancelled.
func Cancel(targetUserData uint64, flags uint32) *CancelOp {
	return &CancelOp{flags: flags, targetUserData: targetUserData}
}

func (op *CancelOp) PrepSQE(sqe *SQEntry) {
	sqe.fill(opAsyncCancel, int32(-1), uintptr(op.targetUserData), 0, 0)
	sqe.OpcodeFlags = op.flags
}

//LinkTimeoutOp IORING_OP_LINK_TIMEOUT command.
type LinkTimeoutOp struct {
	dur time.Duration
}

//LinkTimeout - timeout operation for linked command.
func LinkTimeout(duration time.Duration) *LinkTimeoutOp {
	return &LinkTimeoutOp{
		dur: duration,
	}
}

func (op *LinkTimeoutOp) PrepSQE(sqe *SQEntry) {
	spec := syscall.NsecToTimespec(op.dur.Nanoseconds())
	sqe.fill(opLinkTimeout, -1, uintptr(unsafe.Pointer(&spec)), 1, 0)
}

//RecvOp receive a message from a socket operation.
type RecvOp struct {
	fd       uintptr
	vec      syscall.Iovec
	msgFlags uint32
}

//Recv receive a message from a socket.
func Recv(socketFd uintptr, vec syscall.Iovec, msgFlags uint32) *RecvOp {
	return &RecvOp{
		fd:       socketFd,
		vec:      vec,
		msgFlags: msgFlags,
	}
}

func (op *RecvOp) PrepSQE(sqe *SQEntry) {
	sqe.fill(opRecv, int32(op.fd), uintptr(unsafe.Pointer(op.vec.Base)), uint32(op.vec.Len), 0)
	sqe.OpcodeFlags = op.msgFlags
}

//SendOp send a message to a socket operation.
type SendOp struct {
	fd       uintptr
	vec      syscall.Iovec
	msgFlags uint32
}

//Send send a message to a socket.
func Send(socketFd uintptr, vec syscall.Iovec, msgFlags uint32) *SendOp {
	return &SendOp{
		fd:       socketFd,
		vec:      vec,
		msgFlags: msgFlags,
	}
}

func (op *SendOp) PrepSQE(sqe *SQEntry) {
	sqe.fill(opSend, int32(op.fd), uintptr(unsafe.Pointer(op.vec.Base)), uint32(op.vec.Len), 0)
	sqe.OpcodeFlags = op.msgFlags
}
