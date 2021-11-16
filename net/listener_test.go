//go:build linux
// +build linux

package net

import (
	reactor "github.com/godzie44/go-uring/reactor"
	"github.com/godzie44/go-uring/uring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"sync"
	"testing"
)

func TestListenerAccept(t *testing.T) {
	r, err := uring.New(uring.MaxEntries >> 1)
	require.NoError(t, err)
	defer r.Close()

	reactor, err := reactor.NewNet([]*uring.Ring{r})
	require.NoError(t, err)

	l, err := NewListener(net.ListenConfig{}, "0.0.0.0:8080", reactor)
	require.NoError(t, err)

	connections := make([]net.Conn, 100)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < len(connections); i++ {
			c, err := net.Dial("tcp", "0.0.0.0:8080")
			assert.NoError(t, err)

			connections[i] = c
		}
	}()

	for i := 0; i < 100; i++ {
		_, err = l.Accept()
		assert.NoError(t, err)
	}

	wg.Wait()

	for _, c := range connections {
		assert.NoError(t, c.Close())
	}

	require.NoError(t, l.Close())
}
