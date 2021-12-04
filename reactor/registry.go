package reactor

import (
	"sync"
	"sync/atomic"
)

type (
	cbMap   map[uint32]Callback
	granule struct {
		nonces    []uint32
		callbacks []cbMap
		border    int

		slowCallbacks map[int]cbMap
		slowNonces    map[int]uint32

		sync.Mutex
	}
	cbRegistry struct {
		granules []*granule
		granCnt  int
	}
)

func newGranule(fCap int) *granule {
	buff := make([]cbMap, fCap)
	for i := range buff {
		buff[i] = make(cbMap, 10)
	}

	return &granule{
		callbacks:     buff,
		nonces:        make([]uint32, fCap),
		border:        fCap,
		slowCallbacks: make(map[int]cbMap),
		slowNonces:    make(map[int]uint32),
	}
}

func (g *granule) add(idx int, cb Callback) (n uint32) {
	if idx < g.border {
		n = atomic.AddUint32(&g.nonces[idx], 1)
		g.Lock()
		g.callbacks[idx][n] = cb
		g.Unlock()
		return n
	}

	//slow path, for big fd values
	g.Lock()
	g.slowNonces[idx]++
	n = g.slowNonces[idx]

	if _, exists := g.slowCallbacks[idx]; !exists {
		g.slowCallbacks[idx] = make(cbMap, 10)
	}

	g.slowCallbacks[idx][n] = cb
	g.Unlock()
	return n
}

func (g *granule) pop(idx int, nonce uint32) Callback {
	if idx < g.border {
		g.Lock()
		cb := g.callbacks[idx][nonce]
		delete(g.callbacks[idx], nonce)
		g.Unlock()
		return cb
	}

	g.Lock()
	cb := g.slowCallbacks[idx][nonce]
	delete(g.slowCallbacks[idx], nonce)
	g.Unlock()

	return cb
}

func newCbRegistry(granularity int) *cbRegistry {
	granules := make([]*granule, granularity)
	for i := 0; i < granularity; i++ {
		granules[i] = newGranule((1 << 16) / granularity)
	}

	return &cbRegistry{
		granCnt:  granularity,
		granules: granules,
	}
}

func (r *cbRegistry) add(fd int, cb Callback) uint32 {
	return r.granules[fd%r.granCnt].
		add(fd/r.granCnt, cb)
}

func (r *cbRegistry) pop(fd int, nonce uint32) Callback {
	return r.granules[fd%r.granCnt].
		pop(fd/r.granCnt, nonce)
}
