# KCPTUN package

## Server

```go
// make config
config = server.Config{}
config.Listen = ":4000"
...

// start
server.Start(config)
```



## Client

```go
// make config
config = client.Config{}
config.LocalAddr = ":8388"
...

// start
client.Start(config)
```



## Config

​	Please view [kcptun docment](https://github.com/xtaci/kcptun/blob/master/README.md).



## Demo

​	You can build examples via `make` command in Linux, if you installed Golang development environment. All complied binary file will save to the **bin** directory. With follow command and args, we will start a kcptun server, that will listen at port **4000** and transfer packages from kcptun client to **127.0.0.1:7777 **.

```bash
./bin/server -t "127.0.0.1:7777" -l ":4000" -mode fast2
```

​	 With follow command and args, we will start a kcptun client, that will listen at port **8388** and transfer data to remote kcptun server.

```bash
./bin/client -r "127.0.0.1:4000" -l ":8388" -mode fast2
```

​	All data from **127.0.0.1:8388** will be transfer to **127.0.0.1:7777** via the tunnel.