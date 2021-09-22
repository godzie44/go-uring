package ioring

import (
	"golang.org/x/sys/unix"
	"math"
	"syscall"
	"unsafe"
)

const (
	SYS_IO_URING_SETUP    uintptr = 425
	SYS_IO_URING_ENTER    uintptr = 426
	SYS_IO_URING_REGISTER uintptr = 427

	IORING_SETUP_CQSIZE uint32 = 1 << 3 /* app defines CQ size */

	IORING_FEAT_SINGLE_MMAP uint32 = 1 << 0
	IORING_FEAT_NODROP      uint32 = 1 << 1

	IORING_OFF_CQ_RING uint64 = 0x8000000
	IORING_OFF_SQES    uint64 = 0x10000000

	//copied from signal_unix.numSig
	numSig = 65
)

// sqRing ring flags
const (
	IORING_SQ_NEED_WAKEUP uint32 = 1 << 0 // needs io_uring_enter wakeup
	IORING_SQ_CQ_OVERFLOW uint32 = 1 << 1 // cq ring is overflown
)

// flags
const (
	IORING_ENTER_GETEVENTS uint32 = 1 << 0
)

const (
	libUserDataTimeout = math.MaxUint64
)

func sysEnter(ringFD int, toSubmit uint32, minComplete uint32, flags uint32, sig *unix.Sigset_t) (uint, error) {
	return sysEnter2(ringFD, toSubmit, minComplete, flags, sig, numSig/8)
}

func sysEnter2(ringFD int, toSubmit uint32, minComplete uint32, flags uint32, sig *unix.Sigset_t, sz int) (uint, error) {
	consumed, _, errno := syscall.Syscall6(
		SYS_IO_URING_ENTER,
		uintptr(ringFD),
		uintptr(toSubmit),
		uintptr(minComplete),
		uintptr(flags),
		uintptr(unsafe.Pointer(sig)),
		uintptr(sz),
	)
	if errno != 0 {
		return 0, errno
	}

	return uint(consumed), nil
}

func sysSetup(entries uint32, params *ringParams) (int, error) {
	fd, _, errno := syscall.Syscall(SYS_IO_URING_SETUP, uintptr(entries), uintptr(unsafe.Pointer(params)), 0)
	if errno != 0 {
		return int(fd), errno
	}

	return int(fd), nil
}

type SQEntry struct {
	opcode      uint8
	flags       uint8
	ioPrio      uint16
	fd          int32
	off         uint64
	addr        uint64
	len         uint32
	opcodeFlags uint32
	userData    uint64

	bufIG       uint16
	personality uint16
	spliceFdIn  int32
	_pad2       [2]uint64
}

//go:uintptrescapes
func (sqe *SQEntry) fill(op opcode, fd int32, addr uintptr, len uint32, offset uint64) {
	sqe.opcode = uint8(op)
	sqe.flags = 0
	sqe.ioPrio = 0
	sqe.fd = fd
	sqe.off = offset
	setAddr(sqe, addr)
	//sqe.addr = uint64(addr)
	sqe.len = len
	sqe.opcodeFlags = 0
	sqe.userData = 0
	sqe.bufIG = 0
	sqe.personality = 0
	sqe.spliceFdIn = 0
	sqe._pad2[0] = 0
	sqe._pad2[1] = 0
}

func (sqe *SQEntry) setUserData(ud uint64) {
	sqe.userData = ud
}

//go:uintptrescapes
func setAddr(sqe *SQEntry, addr uintptr) {
	sqe.addr = uint64(addr)
}

type CQEvent struct {
	UserData uint64
	Res      int32
	Flags    uint32
}

func (cqe *CQEvent) Error() error {
	if cqe.Res < 0 {
		return syscall.Errno(uintptr(-cqe.Res))
	}
	return nil
}
