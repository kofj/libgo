package server

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

func TestMain(t *testing.T) {
	println(Start("0.0.0.0:8000", "0.0.0.0:8018", false, "", ""))

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	Stop()
	log.Println("received signal,shutdown")
}
