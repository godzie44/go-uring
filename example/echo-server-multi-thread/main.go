package main

import (
	"flag"
	"fmt"
	uringnet "github.com/godzie44/go-uring/net"
	"github.com/godzie44/go-uring/reactor"
	"github.com/godzie44/go-uring/uring"
	"io"
	"log"
	"net"
	"runtime"
	"strconv"
	"time"
)

type logger struct {
}

func (l *logger) Log(keyvals ...interface{}) {
	log.Println(keyvals...)
}

const MaxConns = 4096
const MaxMsgLen = 2048

func initBuffs() [][]byte {
	buffs := make([][]byte, MaxConns)
	for i := range buffs {
		buffs[i] = make([]byte, MaxMsgLen)
	}
	return buffs
}

var buffs = initBuffs()

const (
	modeURing       = "uring"
	modeURingSQPoll = "uring-sq-poll"
	modeDefault     = "default"
)

var mode = flag.String("mode", "uring", "server async backend: uring/uring-sq-poll/default")
var ringCount = flag.Int("ring-count", 6, "io_uring's count")
var wpCount = flag.Int("wp-count", 2, "io_uring's work pools count")

func main() {
	flag.Parse()
	portStr := flag.Arg(0)
	port, _ := strconv.Atoi(portStr)

	var listener net.Listener
	var err error

	var opts []uring.SetupOption

	switch *mode {
	case modeDefault:
		listener, err = net.ListenTCP("tcp", &net.TCPAddr{
			Port: port,
		})
		checkErr(err)

	case modeURingSQPoll:
		opts = append(opts, uring.WithSQPoll(time.Millisecond*100))
		fallthrough

	case modeURing:
		runtime.GOMAXPROCS(runtime.NumCPU() * 2)

		rings, closeRings, err := uring.CreateMany(*ringCount, uring.MaxEntries>>3, *wpCount, opts...)
		checkErr(err)
		defer func() {
			if err = closeRings(); err != nil {
				log.Fatal(err)
			}
		}()

		netReactor, err := reactor.NewNet(rings, reactor.WithLogger(&logger{}))
		checkErr(err)

		listener, err = uringnet.NewListener(net.ListenConfig{}, fmt.Sprintf("0.0.0.0:%d", port), netReactor)
		checkErr(err)

	default:
		log.Fatal("mode must be one of uring/uring-sq-poll/default")
	}

	if *mode == modeDefault {
		log.Printf("start echo-server, mode: defaut net/http, port: %d", port)
	} else {
		log.Printf("start echo-server, mode: %s, port: %d, rings: %d, pools: %d", *mode, port, *ringCount, *wpCount)
	}

	runServer(listener)
}

func runServer(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		checkErr(err)

		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	var fd int

	switch c := conn.(type) {
	case *net.TCPConn:
		f, _ := c.File()
		fd = int(f.Fd())
	case *uringnet.Conn:
		fd = int(c.Fd())
	}

	buff := buffs[fd]
	for {
		n, err := conn.Read(buff)
		if err == io.EOF || n == 0 {
			checkErr(conn.Close())
			return
		}
		checkErr(err)

		_, err = conn.Write(buff[:n])
		checkErr(err)
	}
}

func checkErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
