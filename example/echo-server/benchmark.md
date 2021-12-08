## Echo-server benchmark

## requirements to run the benchmarks
__Linux 5.7 or higher__

## programs under test
* echo server using an event loop created with go-uring
* echo server using an event loop created with go-uring, using amd64_atomic build tag
* echo server written with C using an event loop created with liburing : https://github.com/frevib/io_uring-echo-server/tree/io-uring-feat-fast-poll

## system specs
* Intel(R) Core(TM) i5-7500 CPU @ 3.40GHz, 16GB RAM, 4 physical cores
* Virtual box with Ubuntu 20.04 (8GB RAM, 4 physical cores)
* Linux 5.16rc3
* 1 core isolated for the echo server with isolcpus=0
* Echo server is assigned a dedicated CPU with taskset -cp 0 [pid]

## benchmark tool
* Rust echo bench: https://github.com/haraldh/rust_echo_bench
* `cargo run --release -- --address "localhost:8080" --number {number of clients} --duration 30 --length {msg size}`
* 5 runs for each combination of 128 and 1024 bytes message size with 100, 500 and 1000 clients
* [bench.sh](#benchmark script) script using for benchmarking

# Results

|                               | c: 100 bytes: 128 | c: 50 bytes: 1024| c: 500 bytes: 128 | c: 500 bytes: 1024| c: 1000 bytes: 128 | c: 1000 bytes: 1024|
|-------------------------------|-------------------|------------------|-------------------|-------------------|--------------------|--------------------|
| io_uring-echo-server (C lang) | 235356            | 224783           | 173670            | 155477            | 149407             |    139987          |
| echo-server                   | 227884            | 222709           | 169001            | 150275            | 143664             |     128783         |
| echo-server + amd64_atomic tag| 232395            | 223299           | 169428            | 151311            | 143884             |     136978         |

### benchmark script

[Source code](https://github.com/godzie44/go-uring/blob/master/example/echo-server/bench.sh)

* $1 - path to server executable
* $2 - path to rust_echo_bench Cargo.toml

Run example:
```bash
  ./bench.sh ./main ~/rust/rust_echo_bench/Cargo.toml 
```
