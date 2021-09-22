package ioring

import (
	"context"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"os"
	"testing"
	"time"
	"unsafe"
)

func TestReactorExecuteReadVCommand(t *testing.T) {
	r, err := NewRing(64)
	assert.NoError(t, err)
	defer r.Close()

	f, err := os.Open("go.mod")
	assert.NoError(t, err)
	defer f.Close()
	cmd, err := ReadV(f, 16)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reactor := NewReactor(r)
	go func() {
		err := reactor.Run(ctx)
		assert.NoError(t, err)
	}()

	err = reactor.Execute(cmd)
	assert.NoError(t, err)

	select {
	case res := <-reactor.Result():
		assert.NoError(t, res.Error())
		reads := res.Command().(*ReadVCommand)
		expected, err := ioutil.ReadFile("go.mod")
		assert.NoError(t, err)

		str := string(unsafe.Slice(reads.IOVecs[0].Base, reads.Size))
		assert.Equal(t, string(expected), str)
	case <-time.After(3 * time.Second):
		assert.Fail(t, "no reads at 3 seconds")
	}
}
