## Echo-server benchmark

## requirements to run the benchmarks
__Linux 5.7 or higher__

## programs under test
* echo server using an epoll
* echo server using an event loop created with go-uring
* echo server written with C using an event loop created with liburing : https://github.com/frevib/io_uring-echo-server/tree/io-uring-feat-fast-poll

## system specs
* Intel(R) Core(TM) i7-10700KF CPU @ 3.80GHz, 32GB RAM
* Virtual box with Ubuntu 24.10 (16GB RAM, 6 physical cores)
* Linux 6.11
* Echo server is assigned a dedicated CPU with cset

## benchmark tool
* Rust echo bench: https://crates.io/crates/tcp-echo-benchmark
* `tcp-echo-benchmark --address "localhost:8080" --number {number of clients} --duration 30 --length {msg size}`
* 5 runs for each combination of 128 and 1024 bytes message size with 100, 500 and 1000 clients
* [bench.sh](#benchmark script) script using for benchmarking

# Results

|                               | c: 100 bytes: 128 | c: 100 bytes: 1024| c: 500 bytes: 128 | c: 500 bytes: 1024| c: 1000 bytes: 128 | c: 1000 bytes: 1024|
|-------------------------------|-------------------|------------------|-------------------|-------------------|--------------------|--------------------|
| epoll echo-server             | 267055            | 259730           | 276911            | 264069            | 274568             |     258904         |
| io_uring echo-server          | 355931            | 310814           | 364609            | 364675            | 363552             |     303197         |
| io_uring-echo-server (C lang) | 379458            | 361902           | 386219            | 367076            | 382633             |    284460          |


### benchmark script

[Source code](https://github.com/godzie44/go-uring/blob/master/example/echo-server/bench.sh)

* $1 - path to server executable
* $2 - path to rust_echo_bench Cargo.toml

Run example:
```bash
  ./bench.sh ./main ~/rust/rust_echo_bench/Cargo.toml 
```
