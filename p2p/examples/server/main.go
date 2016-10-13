package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kofj/libgo/p2p/server"
)

func main() {
	serverAddr := "0.0.0.0:8000"
	serverUdpAddr := "0.0.0.0:8018"
	useSSL := false
	println(server.Start(serverAddr, serverUdpAddr, useSSL, "", ""))

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	server.Stop()
	log.Println("received signal,shutdown")
}
