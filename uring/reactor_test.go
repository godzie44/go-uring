package uring

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"os"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestReactorExecuteReadVCommand(t *testing.T) {
	t.Skipf("currently race")

	r, err := NewRing(64)
	require.NoError(t, err)
	defer r.Close()

	f, err := os.Open("../go.mod")
	require.NoError(t, err)
	defer f.Close()
	op, err := ReadV(f, 16)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	reactor := NewReactor(r)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := reactor.Run(ctx)
		require.NoError(t, err)
	}()

	err = reactor.Execute(op)
	require.NoError(t, err)

	select {
	case res := <-reactor.Result():
		assert.NoError(t, res.Error())
		reads := res.Operation().(*ReadVOp)
		expected, err := ioutil.ReadFile("../go.mod")
		assert.NoError(t, err)

		str := string(unsafe.Slice(reads.IOVecs[0].Base, reads.Size))
		assert.Equal(t, string(expected), str)
	case <-time.After(3 * time.Second):
		assert.Fail(t, "no reads at 3 seconds")
	}

	cancel()
	wg.Wait()
}
