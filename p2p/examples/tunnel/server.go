package main

import (
	"bufio"
	"net"

	"github.com/kofj/libgo/p2p/client"
)

var data []byte

func main() {
	p := client.New(client.DefaultSettings)
	p.ReceiveFunc = func(msg []byte) {
		data = msg
	}
	p.ReplyFunc = replyFunc
	p.Reg("test.88D1C424")
}

func replyFunc() (msg []byte) {

	conn, err := net.Dial("tcp", "127.0.0.1:8080")
	if conn == nil {
		println("conn is nil")
		return
	}
	if err != nil {
		println("Dial error", err.Error())
	}
	_, err = conn.Write(data)
	if err != nil {
		println("Write error", err.Error())
		return
	}
	arr := make([]byte, 1000)
	reader := bufio.NewReader(conn)
	i := 0
	for {

		i++
		size, err := reader.Read(arr)
		if err != nil {
			println(err.Error())
			break
		}
		msg = append(msg, arr[:size]...)

		if size < 1000 {
			break
		}
	}
	conn.Close()

	return
}
