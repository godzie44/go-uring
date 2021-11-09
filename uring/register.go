package uring

import "unsafe"

// io_uring_register(2) opcodes and arguments
const (
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
	//err := sysRegisterProbe(r.fd, probe, 256)
	err := sysRegister(r.fd, sysRingRegisterProbe, unsafe.Pointer(probe), 256)

	return probe, err
}

func (r *Ring) SetIOWQMaxWorkers(count int) error {
	err := sysRegister(r.fd, sysRingRegisterIOWQMaxWorkers, unsafe.Pointer(&count), 2)
	return err
}
