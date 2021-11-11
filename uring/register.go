package uring

import (
	"syscall"
	"unsafe"
)

// io_uring_register(2) opcodes and arguments
const (
	sysRingRegisterBuffers        = 0
	sysRingUnRegisterBuffers      = 1
	sysRingRegisterFiles          = 2
	sysRingUnRegisterFiles        = 3
	sysRingRegisterProbe          = 8
	sysRingRegisterIOWQMaxWorkers = 19
)

type (
	Probe struct {
		lastOp uint8
		opsLen uint8
		_res   uint16
		_res2  [3]uint32
		ops    [256]probeOp
	}
	probeOp struct {
		Op    uint8
		_res  uint8
		Flags uint16
		_res2 uint32
	}
)

const OpSupportedFlag uint16 = 1 << 0

func (p *Probe) GetOP(n int) *probeOp {
	return &p.ops[n]
}

func (r *Ring) Probe() (*Probe, error) {
	probe := &Probe{}
	err := sysRegister(r.fd, sysRingRegisterProbe, unsafe.Pointer(probe), 256)

	return probe, err
}

func (r *Ring) SetIOWQMaxWorkers(count int) error {
	err := sysRegister(r.fd, sysRingRegisterIOWQMaxWorkers, unsafe.Pointer(&count), 2)
	return err
}

func (r *Ring) RegisterBuffers(buffers []syscall.Iovec) error {
	err := sysRegister(r.fd, sysRingRegisterBuffers, unsafe.Pointer(&buffers[0]), len(buffers))
	return err
}

func (r *Ring) UnRegisterBuffers() error {
	err := sysRegister(r.fd, sysRingUnRegisterBuffers, unsafe.Pointer(nil), 0)
	return err
}

func (r *Ring) RegisterFiles(descriptors []int) error {
	err := sysRegister(r.fd, sysRingRegisterFiles, unsafe.Pointer(&descriptors[0]), len(descriptors))
	return err
}

func (r *Ring) UnRegisterFiles() error {
	err := sysRegister(r.fd, sysRingUnRegisterFiles, unsafe.Pointer(nil), 0)
	return err
}
