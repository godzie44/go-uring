//go:build linux
// +build linux

package main

import (
	"context"
	"flag"
	"fmt"
	net "github.com/godzie44/go-uring/net"
	reactor "github.com/godzie44/go-uring/reactor"
	"github.com/godzie44/go-uring/uring"
	"io"
	"log"
	gonet "net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
)

var noURing = flag.Bool("no-uring", false, "dont use io_uring based http server")
var ringCount = flag.Int("r", 1, "io_urings count")

func main() {
	flag.Parse()

	go func() {
		_ = http.ListenAndServe("localhost:6060", nil)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong\n")) //nolint
	})

	mux.HandleFunc("/post", func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer request.Body.Close()

		body, _ := io.ReadAll(request.Body)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf("you send: %s", string(body)))) //nolint
	})

	mux.HandleFunc("/get-file", func(w http.ResponseWriter, request *http.Request) {
		file, err := os.ReadFile("./example/http-server/gopher.png")
		checkErr(err)

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(file) //nolint
	})

	mux.HandleFunc("/file-info", func(w http.ResponseWriter, request *http.Request) {
		request.ParseMultipartForm(10 << 20) //nolint

		file, handler, err := request.FormFile("file")
		checkErr(err)
		defer file.Close()

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Uploaded File: %+v\n", handler.Filename)
		fmt.Fprintf(w, "File Size: %+v\n", handler.Size)
		fmt.Fprintf(w, "MIME Header: %+v\n", handler.Header)
	})

	if *noURing {
		log.Println("start default net/http server")
		defSrv(mux)
	} else {
		log.Println("start io_uring server")
		ringSrv(mux)
	}
}

func defSrv(mux *http.ServeMux) {
	lc := gonet.ListenConfig{}
	l, _ := lc.Listen(context.Background(), "tcp", "0.0.0.0:8080")

	server := &http.Server{
		Handler: mux,
	}
	err := server.Serve(l)

	checkErr(err)
}

func ringSrv(mux *http.ServeMux) {
	ringCnt := *ringCount

	var params []uring.SetupOption
	runtime.GOMAXPROCS(runtime.NumCPU() * 2)

	rings, stop, err := uring.CreateMany(ringCnt, uring.MaxEntries>>2, 1, params...)
	checkErr(err)
	defer stop() //nolint

	reactor, err := reactor.NewNet(rings)
	checkErr(err)

	l, err := net.NewListener(gonet.ListenConfig{}, "0.0.0.0:8080", reactor)
	checkErr(err)

	server := &http.Server{Addr: "0.0.0.0:8080", Handler: mux}
	defer server.Close()

	err = server.Serve(l)
	checkErr(err)
}

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}
