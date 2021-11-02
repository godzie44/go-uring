package uring

import (
	"golang.org/x/sys/unix"
	"math"
	"syscall"
	"unsafe"
)

const (
	sysRingSetup    uintptr = 425
	sysRingEnter    uintptr = 426
	sysRingRegister uintptr = 427

	//copied from signal_unix.numSig
	numSig = 65
)

// sqRing ring flags
const (
	sqNeedWakeup uint32 = 1 << 0 // needs io_uring_enter wakeup
	sqCQOverflow uint32 = 1 << 1 // cq ring is overflown
)

// io_uring_enter flags
const (
	sysRingEnterGetEvents uint32 = 1 << 0
	sysRingEnterSQWakeup  uint32 = 1 << 1
	sysRingEnterSQWait    uint32 = 1 << 2
	sysRingEnterExtArg    uint32 = 1 << 3
)

// io_uring_register(2) opcodes and arguments
const (
	opSupported uint32 = 1 << 0

	sysRingRegisterProbe = 8
)

const (
	libUserDataTimeout = math.MaxUint64
)

func sysEnter(ringFD int, toSubmit uint32, minComplete uint32, flags uint32, sig *unix.Sigset_t) (uint, error) {
	return sysEnter2(ringFD, toSubmit, minComplete, flags, sig, numSig/8)
}

func sysEnter2(ringFD int, toSubmit uint32, minComplete uint32, flags uint32, sig *unix.Sigset_t, sz int) (uint, error) {
	consumed, _, errno := syscall.Syscall6(
		sysRingEnter,
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
	fd, _, errno := syscall.Syscall(sysRingSetup, uintptr(entries), uintptr(unsafe.Pointer(params)), 0)
	if errno != 0 {
		return int(fd), errno
	}

	return int(fd), nil
}

func sysRegisterProbe(ringFD int, probe *Probe, len int) error {
	_, _, errno := syscall.Syscall6(
		sysRingRegister,
		uintptr(ringFD),
		uintptr(sysRingRegisterProbe),
		uintptr(unsafe.Pointer(probe)),
		uintptr(len),
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

type SQEntry struct {
	OpCode      uint8
	Flags       uint8
	IoPrio      uint16
	Fd          int32
	Off         uint64
	Addr        uint64
	Len         uint32
	OpcodeFlags uint32
	UserData    uint64

	BufIG       uint16
	Personality uint16
	SpliceFdIn  int32
	_pad2       [2]uint64
}

//go:uintptrescapes
func (sqe *SQEntry) fill(op opcode, fd int32, addr uintptr, len uint32, offset uint64) {
	sqe.OpCode = uint8(op)
	sqe.Flags = 0
	sqe.IoPrio = 0
	sqe.Fd = fd
	sqe.Off = offset
	setAddr(sqe, addr)
	sqe.Len = len
	sqe.OpcodeFlags = 0
	sqe.UserData = 0
	sqe.BufIG = 0
	sqe.Personality = 0
	sqe.SpliceFdIn = 0
	sqe._pad2[0] = 0
	sqe._pad2[1] = 0
}

func (sqe *SQEntry) setUserData(ud uint64) {
	sqe.UserData = ud
}

//go:uintptrescapes
func setAddr(sqe *SQEntry, addr uintptr) {
	sqe.Addr = uint64(addr)
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

type getEventsArg struct {
	sigMask   uintptr
	sigMaskSz uint32
	_pad      uint32
	ts        uintptr
}

//go:uintptrescapes
func newGetEventsArg(sigMask uintptr, sigMaskSz uint32, ts uintptr) *getEventsArg {
	return &getEventsArg{sigMask: sigMask, sigMaskSz: sigMaskSz, ts: ts}
}
