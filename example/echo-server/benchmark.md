## Echo-server benchmark

## requirements to run the benchmarks
__Linux 5.7 or higher__

## programs under test
* echo server using an event loop created with go-uring
* echo server using an event loop created with go-uring, using amd64_atomic build tag
* echo server written with C using an event loop created with liburing : https://github.com/frevib/io_uring-echo-server/tree/io-uring-feat-fast-poll

## system specs
* Linux 5.11
* Intel(R) Core(TM) i5-7500 CPU @ 3.40GHz, 16GB RAM, 4 physical cores

## benchmark tool
* Rust echo bench: https://github.com/haraldh/rust_echo_bench
* `cargo run --release -- --address "localhost:8080" --number {number of clients} --duration 30 --length {msg size}`
* 5 runs for each combination of 128 and 1024 bytes message size with 50, 500 and 1000 clients
* [bench.sh](#benchmark script) script using for benchmarking

# Results

|                               | c: 50 bytes: 128 | c: 50 bytes: 1024| c: 500 bytes: 128 | c: 500 bytes: 1024| c: 1000 bytes: 128 | c: 1000 bytes: 1024|
|-------------------------------|------------------|------------------|-------------------|-------------------|--------------------|--------------------|
| io_uring-echo-server (C lang) | 256381           | 252801           | 201674            | 190072            |     167620         |    168565          |
| echo-server                   | 254159           | 249254           | 198436            | 188312            |  165763            |     166539         |
| echo-server + amd64_atomic tag| 256011           | 250045           | 198938            | 186052            |  166462            |     167237         |

### benchmark script

[Source code](https://github.com/godzie44/go-uring/blob/master/example/echo-server/bench.sh)

* $1 - path to server executable
* $2 - path to rust_echo_bench Cargo.toml

Run example:
```bash
  ./bench.sh ./main ~/rust/rust_echo_bench/Cargo.toml 
```
