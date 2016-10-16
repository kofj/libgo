# P2P

## Server

​	Server use for clients link to each other in p2p mode, or transfer data in c/s mode. Server example:

```go
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
```



## Client

​	You can register a service via `Reg` method or connect to a remote service via `Link` method.

### Register a Service

​	Full demo: [tunnel server](p2p/examples/tunnel/server.go)

```go
s := client.DefaultSettings
// print logs
s.Verbose = true
// set servers address
s.RemoteAddr = "127.0.0.1:8000"
s.BusterAddr = "127.0.0.1:8018"

// new client instance
p := client.New(s)

// process msg from a link
p.ReceiveFunc = func(msg []byte) {
	data = msg
}

// reply to the link 
p.ReplyFunc = replyFunc

// register servicea with unique name
p.Reg("test.88D1C424")

// print error logs when exit.
err := p.GetError()
if err != nil {
	println(err.Error())
}
println("exit.")
```



### Link to the service.

​	Full demo: [tunnel client](p2p/examples/tunnel/client.go)

```go
s := client.DefaultSettings
// s.ClientMode = 2 // c/s mode
s.RemoteAddr = "127.0.0.1:8000"
s.BusterAddr = "127.0.0.1:8018"

// new client instance
p := client.New(s)

// set function to process data from the service
p.ReceiveFunc = receiveFunc
// set function to send data to the service
p.SendFunc = sendFunc

// link to service
p.Link("test.88D1C424")

// print error logs when exit
if err := p.GetError(); err != nil {
	println(err.Error())
}
println("exit.")
```



### Settings

```go
BusterAddr string // MakeHole server
RemoteAddr string // connect remote server
PipeNum    int    // pipe num for transmission
ClientKey  string // when other client linkt to the reg client, need clientkey, or empty
ClientMode int    // connect mode:0 if p2p fail, use c/s mode;1 just p2p mode;2 just c/s mode
UseSSL     bool   // use ssl
Verbose    bool   // verbose mode
InitAddr   string // addip for bust,xx.xx.xx.xx;xx.xx.xx.xx;
```



## Demo

