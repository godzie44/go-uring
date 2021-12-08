## GO-URING

### About
This project is a port of [liburing](https://github.com/axboe/liburing) for GO.

The project contains:
1. [uring package](#uring-package) - low-level io_uring API. This API is similar to libruing API.
2. [reactor package](#reactor-package) - high-level API - realization of event loop pattern with io_uring.
3. [net](#net-package) - this is a realization of net.Listener and net.Conn interfaces with io_uring. This can be used, for example, to run an HTTP server with io_uring inside.
4. Examples and benchmarks:
   1. [Plain echo server](#plain-tcp-echo-server)
   2. [GO-style echo server (multi thread/goroutine)](#tcp-echo-server)
### uring package

Package uring is a port of liburing. It provides low-level functionality for working with io_uring.
Example of usage:

- Read file:
```GO
// create io_uring instance
ring, err := uring.New(8)
noErr(err)
defer ring.Close()

// open file and init read buffers
file := ... 

// add ReadV operation to SQ queue
err = ring.QueueSQE(uring.ReadV(f, vectors, 0), 0, 0)
noErr(err)

// submit all SQ new entries
_, err = ring.Submit()
noErr(err)

// wait until data is reading into buffer
cqe, err := ring.WaitCQEvents(1)
noErr(err)

// dequeue CQ
ring.SeenCQE(cqe)

fmt.Println("read %d bytes, read result: %s", cqe.Res, vectors)
```

- Accept incoming connections:
```GO
// create io_uring instance
ring, err := uring.New(8)
noErr(err)
defer ring.Close()

// create server socket
socketFd := ...

for {
    // add Accept operation to SQ queue
    err = ring.QueueSQE(uring.Accept(socketFd, 0), 0, 0)
    noErr(err)

    // submit all SQ new entries
    _, err = ring.Submit()
    noErr(err)

    // wait until new client connection is accepted
    cqe, err := ring.WaitCQEvents(1)
    noErr(err)
    
    // handle client socket, socket descriptor now in cqe.Res field
    handleNewConnection(cqe.Res)
    
    // dequeue CQ
    ring.SeenCQE(cqe)
}
```

- Look more examples in tests and example folder

#### Release/Acquire semantic

Model of GO atomic is more strict than atomics using in liburing. Currently, there is no public description of memory model for GO atomics, 
but with this articles ([1](https://research.swtch.com/gomm), [2](https://github.com/golang/go/issues/5045)), we know that the implementation of GO atomics is the same as default (seq_cst) atomics in C/C++. 
But liburing use less strict semantic (explained [here](https://kernel.dk/io_uring.pdf)) - similar memory_order_acquire/memory_order_release semantic in C/C++ memory model. Certainly, we can use
GO atomics as is (because of strict semantic), but this entails some overhead costs.

This lib provides experimental realization and totally unsafe of memory_order_acquire/memory_order_release atomics for amd64 arch, you can enable it
by adding build tag amd64_atomic. It is based on the fact that MOV instructions are enough to implement memory_order_acquire/memory_order_release on the amd64 architecture ([link](https://www.cl.cam.ac.uk/~pes20/cpp/cpp0xmappings.html)). For example:

```sh
  go test -v -tags amd64_atomic ./...
```

This can give about 1%-3% performance gain.

### reactor package 

Reactor - is event loop with io_uring inside it. Currently, there are two reactors in this package:
1. Reactor - generic event loop, give a possibility to work with all io_uring operations.
2. NetReactor - event loop optimized for work with network operations. Currently, support Accept, Recv, and Send.

### net package

This is the realization of net.Listener and net.Conn interfaces. Using NetReactor inside. Please check the example of HTTP server and benchmarks to familiarize yourself with it.

### Examples and benchmarks

#### Plain TCP echo-server

Single thread echo-server (listens on a specific TCP port and as soon as any data arrives at this port, it immediately forwards it back to the sender) implemented with go-uring. 
Using for compare GO go-uring lib realization with liburing realization.
See [source code](https://github.com/godzie44/go-uring/blob/master/example/echo-server/main.go) and [benchmarks](https://github.com/godzie44/go-uring/blob/master/example/echo-server/benchmark.md) for familiarization.

#### GO-style TCP echo-server

Echo-server (listens on a specific TCP port and as soon as any data arrives at this port, it immediately forwards it back to the sender) implemented with go-uring. 
Realization similar with realization of echo-server with net/http package (benchmarks attached).
See [source code](https://github.com/godzie44/go-uring/blob/master/example/echo-server-multi-thread/main.go) and [benchmarks](https://github.com/godzie44/go-uring/blob/master/example/echo-server-multi-thread/benchmark.md) for familiarization.

#### HTTP server