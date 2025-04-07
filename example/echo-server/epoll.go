package main

import (
	"flag"
	"log"
	"net"
	"strconv"

	"golang.org/x/sys/unix"
)

const (
	MaxEvents = 1024
	BufferSize = 2048
)

var buf = make([]byte, BufferSize)

func main() {
	flag.Parse()
	port, _ := strconv.Atoi(flag.Arg(0))

    sockFd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	checkErr("Error creating socket:", err)
    defer unix.Close(sockFd)

    sockaddr := &unix.SockaddrInet4{Port: port}
    copy(sockaddr.Addr[:], net.ParseIP("127.0.0.1").To4())

    err = unix.Bind(sockFd, sockaddr)
	checkErr("Error binding socket:", err)

    err = unix.Listen(sockFd, unix.SOMAXCONN)
	checkErr("Error listening on socket:", err)

    err = unix.SetNonblock(sockFd, true)
	checkErr("Error setting socket to non-blocking:", err)

    epFd, err := unix.EpollCreate1(0)
	checkErr("Error creating epoll:", err)
    defer unix.Close(epFd)

    err = unix.EpollCtl(epFd, unix.EPOLL_CTL_ADD, sockFd, &unix.EpollEvent{Events: unix.EPOLLIN, Fd: int32(sockFd)})
	checkErr("Error adding socket to epoll:", err)

    events := make([]unix.EpollEvent, MaxEvents)

    for {
        n, err := unix.EpollWait(epFd, events, -1)
        if err != nil {
            if err == unix.EINTR {
                continue
            }
			checkErr("Error waiting for epoll:", err)
        }

        for i := 0; i < n; i++ {
            if events[i].Fd == int32(sockFd) {
                connFd, _, err := unix.Accept4(sockFd, unix.SOCK_NONBLOCK)
                if err != nil {
                    log.Printf("Error accepting connection:", err)
                    continue
                }

                err = unix.EpollCtl(epFd, unix.EPOLL_CTL_ADD, connFd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLET, Fd: int32(connFd)})
				checkErr("Error adding connection to epoll:", err)
            } else {
                handleConnection(int(events[i].Fd))
            }
        }
    }
}

func handleConnection(fd int) {
	n, err := unix.Read(fd, buf)
	checkErr("Failed to read from connection: %v", err)

	if n == 0 {
		return
	}

	_, err = unix.Write(fd, buf[:n])
	checkErr("Failed to write to connection: %v", err)
}

func checkErr(pattern string, err error) {
	if err != nil {
		log.Fatalf(pattern, err)
	}
}
