## GO-URING

Linux io_uring for GO.

## About
This project contains:
1. [uring](#uring-package) package - low-level io_uring API. This API is similar to [liburing](https://github.com/axboe/liburing) API. In other words - this is a port of [liburing](https://github.com/axboe/liburing).
2. [reactor](#reactor-package) package - high-level API - implementation of event loop pattern with io_uring.
3. [net](#net-package) package - this is an implementation of net.Listener and net.Conn interfaces with io_uring.
4. Examples and benchmarks:
   * [Plain echo server](#plain-tcp-echo-server)
   * [GO-style echo server](#go-style-tcp-echo-server) (multi thread/goroutine)
   * [HTTP server](#http-server)

## URING package

Package uring is a port of liburing. It provides low-level functionality for working with io_uring.
Example of usage:

- read file:
```GO
package main

import (
   "fmt"
   "github.com/godzie44/go-uring/uring"
   "os"
)

func main() {
   ring, err := uring.New(8)
   noErr(err)
   defer ring.Close()

   // open file and init read buffers
   file, err := os.Open("./go.mod")
   noErr(err)
   stat, _ := file.Stat()
   buff := make([]byte, stat.Size())

   // add Read operation to SQ queue
   err = ring.QueueSQE(uring.Read(file.Fd(), buff, 0), 0, 0)
   noErr(err)

   // submit all SQ new entries
   _, err = ring.Submit()
   noErr(err)

   // wait until data is reading into buffer
   cqe, err := ring.WaitCQEvents(1)
   noErr(err)

   noErr(cqe.Error()) //check read error

   fmt.Printf("read %d bytes, read result: \n%s", cqe.Res, string(buff))

   // dequeue CQ
   ring.SeenCQE(cqe)
}
```

- accept incoming connections:
```GO
package main

import (
   "fmt"
   "github.com/godzie44/go-uring/uring"
   "syscall"
)

func main() {
   // create io_uring instance
   ring, err := uring.New(8)
   noErr(err)
   defer ring.Close()

   // create server socket
   socketFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
   noErr(err)
   defer syscall.Close(socketFd)

   addr := syscall.SockaddrInet4{Port: 8081}
   noErr(syscall.Bind(socketFd, &addr))
   noErr(syscall.Listen(socketFd, syscall.SOMAXCONN))

   for {
      // add Accept operation to SQ queue
      err = ring.QueueSQE(uring.Accept(uintptr(socketFd), 0), 0, 0)
      noErr(err)

      // submit all SQ new entries
      _, err = ring.Submit()
      noErr(err)

      // wait until new client connection is accepted
      cqe, err := ring.WaitCQEvents(1)
      noErr(err)

      //check accept error
      noErr(cqe.Error())

      // handle client socket, socket descriptor now in cqe.Res field
      fmt.Printf("socket %d connected\n", cqe.Res)

      // dequeue CQ
      ring.SeenCQE(cqe)
   }
}
```

- Look more examples in uring package tests

#### Release/Acquire semantic

Model of GO atomic is more strict than atomics using in liburing. Currently, there is no public description of memory model for GO atomics, 
but with these articles ([1](https://research.swtch.com/gomm), [2](https://github.com/golang/go/issues/5045)), we know that the implementation of GO atomics is the same as default (seq_cst) atomics in C/C++. 
But liburing use less strict semantic (explained [here](https://kernel.dk/io_uring.pdf)) - similar memory_order_acquire/memory_order_release semantic in C/C++ memory model. Certainly, we can use
GO atomics as is (because of strict semantic), but this entails some overhead costs.

This lib provides experimental and totally unsafe realization of memory_order_acquire/memory_order_release atomics for amd64 arch, you can enable it
by adding build tag amd64_atomic. It is based on the fact that MOV instructions are enough to implement memory_order_acquire/memory_order_release on the amd64 architecture ([link](https://www.cl.cam.ac.uk/~pes20/cpp/cpp0xmappings.html)). For example:

```sh
  go test -v -tags amd64_atomic ./...
```

This can give about 1%-3% performance gain.

## REACTOR package 

Reactor - is event loop implemented with io_uring. Currently, there are two reactors in this package:
1. Reactor - generic event loop, give a possibility to work with all io_uring operations.
2. NetReactor - event loop optimized for work with network operations on sockets.

Example of usage:
- read file:
```GO
package main

import (
	"context"
	"fmt"
	"github.com/godzie44/go-uring/reactor"
	"github.com/godzie44/go-uring/uring"
	"os"
	"os/signal"
)

func main() {
	ring, err := uring.New(8)
	noErr(err)

	// create and start reactor
	rea, err := reactor.New([]*uring.Ring{ring})
	noErr(err)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		rea.Run(ctx)
	}()

	// open file and init read buffers
	file, err := os.Open("./go.mod")
	noErr(err)
	stat, _ := file.Stat()
	buff := make([]byte, stat.Size())

	// queue read operation, result CQE will be handled by callback func
	op := uring.Read(file.Fd(), buff, 0)
	rea.Queue(op, func(event uring.CQEvent) {
		noErr(event.Error()) //check read error
		fmt.Printf("read %d bytes, read result: \n%s", event.Res, string(buff))
		cancel()
	})

	<-ctx.Done()
}
```

- accept incoming connections:
```GO
package main

import (
	"context"
	"fmt"
	"github.com/godzie44/go-uring/reactor"
	"github.com/godzie44/go-uring/uring"
	"syscall"
)

func main() {
	ring, err := uring.New(8)
	noErr(err)

	// create and start net-reactor
	rea, err := reactor.NewNet([]*uring.Ring{ring})
	noErr(err)
	go func() {
		rea.Run(context.Background())
	}()

	// create server socket
	socketFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	noErr(err)
	defer syscall.Close(socketFd)
	addr := syscall.SockaddrInet4{Port: 8081}
	noErr(syscall.Bind(socketFd, &addr))
	noErr(syscall.Listen(socketFd, syscall.SOMAXCONN))

	acceptChan := make(chan int32)
	for {
		// queue accept operation, result CQE will be handled by callback func
		op := uring.Accept(uintptr(socketFd), 0)
		rea.Queue(op, func(event uring.CQEvent) {
			noErr(event.Error()) //check accept error
			acceptChan <- event.Res
		})

		fmt.Printf("socket %d connected\n", <-acceptChan)
	}
}
```
- Look more examples in reactor package tests

## NET package

This is the implementation of net.Listener and net.Conn interfaces. Uses NetReactor inside. Please check the example of [HTTP server](#http-server) and [multi thread TCP echo-server](#go-style-tcp-echo-server) to familiarize yourself with it.

## Examples and benchmarks

#### Plain TCP echo-server

Single thread echo-server (listens on a specific TCP port and as soon as any data arrives at this port, it immediately forwards it back to the sender) implemented with go-uring.
Useful for compare GO go-uring lib realization with liburing realization.
See [source code](https://github.com/godzie44/go-uring/blob/master/example/echo-server/main.go) and [benchmarks](https://github.com/godzie44/go-uring/blob/master/example/echo-server/benchmark.md) for familiarization.

#### GO-style TCP echo-server

Echo-server (listens on a specific TCP port and as soon as any data arrives at this port, it immediately forwards it back to the sender) implemented with go-uring and reactor packages. 
Realization similar with realization of echo-server with net/http package (benchmarks attached).
See [source code](https://github.com/godzie44/go-uring/blob/master/example/echo-server-multi-thread/main.go) and [benchmarks](https://github.com/godzie44/go-uring/blob/master/example/echo-server-multi-thread/Benchmark.md) for familiarization.

#### HTTP server

Example of HTTP-server implemented with io_uring. [Sources](https://github.com/godzie44/go-uring/blob/master/example/http-server/main.go).