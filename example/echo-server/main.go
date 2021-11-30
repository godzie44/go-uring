//go:build linux
// +build linux

package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/godzie44/go-uring/uring"
	"log"
	"strconv"
	"syscall"
)

const MaxConns = 4096
const Backlog = 512
const MaxMsgLen = 2048

type connType int

const (
	_ connType = iota
	ACCEPT
	READ
	WRITE
)

//Each active connection in this application is described by a connInfo structure.
//fd is the file descriptor for the socket.
//typ - describes the state in which the socket is - waiting for accept, read or write.
type connInfo struct {
	fd  int
	typ connType

	sendOp *uring.SendOp
	recvOp *uring.RecvOp
}

//For each connection, preallocate read and write operations from the socket.
//Reusing operations for reading / writing from the socket reduce the load on the GC
func makeConns() [MaxConns]connInfo {
	var conns [MaxConns]connInfo
	for fd := range conns {
		conns[fd].recvOp = uring.Recv(uintptr(fd), nil, 0)
		conns[fd].sendOp = uring.Send(uintptr(fd), nil, 0)
	}
	return conns
}

//Connection buffer.
var conns = makeConns()

//For each connection initialize a read / write buffer.
func makeBuffers() [][]byte {
	buffs := make([][]byte, MaxConns)
	for i := range buffs {
		buffs[i] = make([]byte, MaxMsgLen)
	}
	return buffs
}

var buffs = makeBuffers()

func main() {
	flag.Parse()
	port, _ := strconv.Atoi(flag.Arg(0))

	// Create a server socket and listen port.
	// Note that when creating a socket we DO NOT SET the O_NON_BLOCK flag,
	// but at the same time all reads and writes will not block the application.
	// This happens because io_uring quietly turns blocking socket operations into non-block system calls.
	serverSockFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	checkErr(err)
	defer syscall.Close(serverSockFd)

	checkErr(syscall.SetsockoptInt(serverSockFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1))

	checkErr(syscall.Bind(serverSockFd, &syscall.SockaddrInet4{
		Port: port,
	}))
	checkErr(syscall.Listen(serverSockFd, Backlog))

	fmt.Printf("io_uring echo server listening for connections on port: %d\n", port)

	//Create an io_uring instance, don't use any custom options.
	//The capacity of the SQ and CQ queues is specified as 4096 entries.
	ring, err := uring.New(4096)
	checkErr(err)
	defer ring.Close()

	//Check for the IORING_FEAT_FAST_POLL feature.
	//For us, this is the most "performing" feature in this application,
	//in fact it is a built-in I/O polling engine in io_uring.
	if !ring.Params.FastPollFeature() {
		checkErr(errors.New("IORING_FEAT_FAST_POLL not available"))
	}

	// Add the first operation to SQ - listen to the server socket to receive incoming connections.
	acceptOp := uring.Accept(uintptr(serverSockFd), 0)
	addAccept(ring, acceptOp)

	cqes := make([]*uring.CQEvent, Backlog)

	var cqe *uring.CQEvent
	for {
		//Submit all SQEs that were added in the previous iteration.
		_, err = ring.Submit()
		checkErr(err)

		//Wait for at least one CQE to appear in the CQ buffer.
		_, err = ring.WaitCQEvents(1)
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			continue
		}
		checkErr(err)

		//Put all the "ready" CQE in the cqes buffer.
		n := ring.PeekCQEventBatch(cqes)

		for i := 0; i < n; i++ {
			cqe = cqes[i]

			checkErr(cqe.Error())

			//In the user_data field, we put the structure index in advance
			//which contains service information about the socket.
			ud := conns[cqe.UserData]
			typ := ud.typ
			res := cqe.Res

			ring.SeenCQE(cqe)

			//Using the type, we identify the operation to which the CQE belongs (accept / recv / send).
			switch typ {
			case ACCEPT:
				addRead(ring, int(res))
				addAccept(ring, acceptOp)
			case READ:
				if res <= 0 {
					_ = syscall.Shutdown(ud.fd, syscall.SHUT_RDWR)
				} else {
					addWrite(ring, ud.fd, res)
				}
			case WRITE:
				addRead(ring, ud.fd)
			}
		}
	}
}

//Put the accept operation in SQ, fd is the descriptor of server socket.
func addAccept(ring *uring.Ring, acceptOp *uring.AcceptOp) {
	conns[acceptOp.Fd()].fd = acceptOp.Fd()
	conns[acceptOp.Fd()].typ = ACCEPT

	err := ring.QueueSQE(acceptOp, 0, uint64(acceptOp.Fd()))
	checkErr(err)
}

//Place recv operation in SQ.
func addRead(ring *uring.Ring, fd int) {
	buff := buffs[fd]

	ci := &conns[fd]
	ci.fd = fd
	ci.typ = READ
	ci.recvOp.SetBuffer(buff)

	err := ring.QueueSQE(ci.recvOp, 0, uint64(fd))
	checkErr(err)
}

//Place send operation in SQ buffer.
func addWrite(ring *uring.Ring, fd int, bytesRead int32) {
	buff := buffs[fd]

	ci := &conns[fd]
	ci.fd = fd
	ci.typ = WRITE
	ci.sendOp.SetBuffer(buff[:bytesRead])

	err := ring.QueueSQE(ci.sendOp, 0, uint64(fd))
	checkErr(err)
}

func checkErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
