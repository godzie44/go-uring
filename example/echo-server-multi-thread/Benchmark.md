## GO echo-server benchmark

## requirements to run the benchmarks
__Linux 5.11 or higher__

## programs under test
* echo-server using go-uring net-reactor as I/O backend
* echo-server using go-uring net-reactor as I/O backend with SQ_POLL enabled
* echo-server built with amd64_atomic tag using go-uring net-reactor as I/O backend
* echo-server using net package (net.TCPListener and net.TCPConn) - with netpoller - default GO I/O backend

Check [main.go](https://github.com/godzie44/go-uring/blob/master/example/echo-server-multi-thread/main.go) for familiarization.

## system specs
* Intel(R) Core(TM) i5-7500 CPU @ 3.40GHz, 16GB RAM, 4 physical cores
* Virtual box with Ubuntu 20.04 (8GB RAM, 4 physical cores)
* Linux 5.16rc3

## benchmark tool
* echo server and benchmarks run at virtual machine as is
* Rust echo bench: https://github.com/haraldh/rust_echo_bench
* `cargo run --release -- --address "localhost:8080" --number {number of clients} --duration 60 --length {msg size}`
* 5 runs for each combination of 128 and 1024 bytes message size with 100, 500 and 1000 clients
* [bench.sh](#benchmark-script) script using for benchmarking

# Results

### Run echo server and benchmarks on 4 CPU cores

|                             | c: 100 bytes: 128 | c: 100 bytes: 1024 | c: 500 bytes: 128 | c: 500 bytes: 1024 | c: 1000 bytes: 128 | c: 1000 bytes: 1024 |
|-----------------------------|-------------------|--------------------|-------------------|--------------------|--------------------|---------------------|
| net/http                    | 132664            | 139206             | 133039            | 139171             | 133480             | 139617              |
| go-uring                    | 18407             | 18509              | 141957            | 138027             | 153726             | 148659              |
| go-uring + amd64_atomic tag | 18448             | 18654              | 141967            | 138185             | 152377             | 148990              |
| go-uring SQ_POLL mode       | 21880             | 22706              | 132270            | 126433             | 139575             | 134885              |

### Q: why go-uring so bad for 100 connections?

As you can see in benchmarks there is a bad performance for go-uring echo-servers on 100-connections test cases. This is not
a problem of io_uring, there is a problem of current net-reactor implementation. For 100-conn's test case echo-server's with go-uring does not load the processor completely,
maybe I fix this in the future. Anyway, goal of this
benchmark is not a win GO netpoller, it's a show that io_uring can be an alternative of netpoller, and in some cases, it also can win netpoller.

By the way, there is a little fix for bad performance on 100-conn's - change startup parameters for creating less io_uring(-ring-count flag) instances at echo-server.

### Benchmark script

Echo-server's startup parameters:
- net/http - ./main -mode default
- go-uring - ./main -mode uring -ring-count 6 -wp-count 2
- go-uring + amd64_atomic tag - ./main -mode uring -ring-count 6 -wp-count 2
- go-uring SQ_POLL mode + amd64_atomic tag - ./main -mode uring-sq-poll -ring-count 2 -wp-count 2

[Source code](https://github.com/godzie44/go-uring/blob/master/example/echo-server-multi-thread/bench.sh)

* $1 - path to executable (with the necessary options)
* $2 - path to rust_echo_bench Cargo.toml

Run example:
```bash
  ./bench.sh "./main -mode uring -ring-count 4 -wp-count 4" ~/rust/rust_echo_bench/Cargo.toml 
```