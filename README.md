## GO-URING.

### About
This project is a port of [liburing](https://github.com/axboe/liburing) for GO.

The project contains three packages:
1. uring - low-level io_uring API. This API is similar to libruing API.
2. reactor - high-level API - realization of event loop pattern with io_uring.
3. net - this is a realization of net.Listener and net.Conn interfaces with io_uring. This can be used, for example, to run HTTP server with io_uring inside.

### uring package

### reactor package

### net package

Project under construction. Check tests for familiarization with the current functionality.