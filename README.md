## GO-URING.

### About
This project is a port of [liburing](https://github.com/axboe/liburing) for GO.

The project contains three packages:
1. uring - low-level io_uring API. This API is similar to libruing API.
2. reactor - high-level API - realization of event loop pattern with io_uring.
3. net - this is a realization of net.Listener and net.Conn interfaces with io_uring. This can be used, for example, to run HTTP server with io_uring inside.

### uring package

Package uring - is low level package, it is port of liburing.
Example of usage:

- Read file:
```GO
    // create io_uring instance
	ring, err := New(8)
	noErr(err)
	defer ring.Close()

	// open file and init read buffers
    ...
	
	// add ReadV operation to SQ queue
	err = ring.QueueSQE(ReadV(f, vectors, 0), 0, 0)
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
    ring, err := New(8)
    noErr(err)
    defer ring.Close()

	// create server socket
	...

	for {
        // add Accept operation to SQ queue
        err = ring.QueueSQE(Accept(socketFd, 0), 0, 0)
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

- And more examples in test, or example folder

### reactor package

Reactor - is event loop with io_uring inside it. Currently there two reactors in this package:
1. Reactor - generic event loop, give attempt to work with all io_uring operations.
2. NetReactor - event loop optimized for work with network operations. Currently support Accept, Recv and Send.

### net package

This is realization of net.Listener and net.Conn interfaces. Its exists NetReactor inside. Please check example of http server and benchmarks for familirize with it.

Project under construction. Check tests for familiarization with the current functionality.