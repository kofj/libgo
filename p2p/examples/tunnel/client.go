package main

import (
	"bufio"
	"net"
	"sync"

	"github.com/kofj/libgo/p2p/client"
)

var (
	count       int
	conns       = make(map[int]*net.Conn)
	mapLock     sync.RWMutex
	acceptChan  = make(chan int, 100)
	receiveChan = make(chan int, 100)
)

func main() {
	// local server listen
	ln, err := net.Listen("tcp", "127.0.0.1:8888")
	if err != nil {
		println(err.Error())
		return
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				println(err.Error())
				continue
			} else {
				mapLock.Lock()
				conns[count] = &conn
				mapLock.Unlock()
				acceptChan <- count
				count++
			}
		}
	}()

	// connect to remote
	s := client.DefaultSettings
	// s.ClientMode = 2
	s.RemoteAddr = "127.0.0.1:8000"
	s.BusterAddr = "127.0.0.1:8018"
	p := client.New(s)
	p.ReceiveFunc = receiveFunc
	p.SendFunc = sendFunc
	p.Link("test.88D1C424")

	if err := p.GetError(); err != nil {
		println(err.Error())
	}
	println("exit.")
}

func receiveFunc(msg []byte) {
	go func() {
		var rId int
		rId = <-receiveChan
		conn := *conns[rId]

		n, err := conn.Write(msg)
		if err != nil {
			println(rId, "Write back error: ", n, err.Error())
		} else {
			println(rId, "Write back ok")
		}
		conn.Close()
	}()
}

func sendFunc(mode string, write func(msg []byte) error) {
	var sId int
	println("link mode is: ", mode)
	for {
		sId = <-acceptChan

		go func() {
			arr := make([]byte, 1e6)
			conn := *conns[sId]
			reader := bufio.NewReader(conn)
			for {
				size, err := reader.Read(arr)
				if err != nil {
					break
				}
				if write(arr[:size]) != nil {
					break
				}
			}

		}()

		receiveChan <- sId
	}

	return
}
