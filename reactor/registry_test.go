package reactor

import (
	"github.com/godzie44/go-uring/uring"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestRegistry(t *testing.T) {
	registry := newCbRegistry(2)

	type tcItem struct {
		fd     int
		addCnt int
	}
	type testCase []tcItem

	testCases := []testCase{
		{{100, 3}},
		{{101, 3}, {102, 3}},
		{{1 << 17, 3}, {1<<17 + 1, 4}, {1<<17 + 2, 1000000}},
	}

	var cb Callback = func(event uring.CQEvent) {
	}

	for _, tc := range testCases {
		var nonces = map[int][]uint32{}

		for _, item := range tc {
			fd := item.fd

			for i := 0; i < item.addCnt; i++ {
				n := registry.add(fd, cb)
				nonces[fd] = append(nonces[fd], n)
			}
		}

		for _, item := range tc {
			fd := item.fd

			assert.Nil(t, registry.pop(fd, uint32(item.addCnt+1)))
			for i := 0; i < item.addCnt; i++ {
				cb := registry.pop(fd, nonces[fd][i])
				assert.NotNil(t, cb)
			}
			assert.Nil(t, registry.pop(fd, uint32(item.addCnt-1)))
		}
	}
}

func BenchmarkRegistry(b *testing.B) {
	r := newCbRegistry(6)

	var cb Callback
	fds := []int{
		1, 2, 3, 4, 5, 6, 1 << 14, 1<<14 + 1, 1<<14 + 2, 1 << 14, 1 << 15,
	}

	for i := 0; i < b.N; i++ {
		for _, fd := range fds {
			for j := 0; j < 10; j++ {
				r.add(fd, cb)
			}
		}
		for _, fd := range fds {
			for j := 0; j < 10; j++ {
				r.pop(fd, uint32(j))
			}
		}
	}
}
