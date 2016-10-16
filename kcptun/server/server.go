package server

import (
	"crypto/sha1"
	"io"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/pbkdf2"

	"github.com/golang/snappy"
	kcp "github.com/xtaci/kcp-go"
	"github.com/xtaci/smux"
)

var (

	// SALT is use for pbkdf2 key expansion
	SALT = "kcp-go"
)

type compStream struct {
	conn net.Conn
	w    *snappy.Writer
	r    *snappy.Reader
}

func (c *compStream) Read(p []byte) (n int, err error) {
	return c.r.Read(p)
}

func (c *compStream) Write(p []byte) (n int, err error) {
	n, err = c.w.Write(p)
	err = c.w.Flush()
	return n, err
}

func (c *compStream) Close() error {
	return c.conn.Close()
}

func newCompStream(conn net.Conn) *compStream {
	c := new(compStream)
	c.conn = conn
	c.w = snappy.NewBufferedWriter(conn)
	c.r = snappy.NewReader(conn)
	return c
}

// handle multiplex-ed connection
func handleMux(conn io.ReadWriteCloser, config *Config) {
	// stream multiplex
	smuxConfig := smux.DefaultConfig()
	smuxConfig.MaxReceiveBuffer = config.SockBuf
	mux, err := smux.Server(conn, smuxConfig)
	if err != nil {
		log.Println(err)
		return
	}
	defer mux.Close()
	for {
		p1, err := mux.AcceptStream()
		if err != nil {
			log.Println(err)
			return
		}
		p2, err := net.DialTimeout("tcp", config.Target, 5*time.Second)
		if err != nil {
			p1.Close()
			log.Println(err)
			continue
		}
		if err := p2.(*net.TCPConn).SetReadBuffer(config.SockBuf); err != nil {
			log.Println("TCP SetReadBuffer:", err)
		}
		if err := p2.(*net.TCPConn).SetWriteBuffer(config.SockBuf); err != nil {
			log.Println("TCP SetWriteBuffer:", err)
		}
		go handleClient(p1, p2)
	}
}

func handleClient(p1, p2 io.ReadWriteCloser) {
	log.Println("stream opened")
	defer log.Println("stream closed")
	defer p1.Close()
	defer p2.Close()

	// start tunnel
	p1die := make(chan struct{})
	go func() { io.Copy(p1, p2); close(p1die) }()

	p2die := make(chan struct{})
	go func() { io.Copy(p2, p1); close(p2die) }()

	// wait for tunnel termination
	select {
	case <-p1die:
	case <-p2die:
	}
}

func checkError(err error) {
	if err != nil {
		log.Printf("%+v\n", err)
		os.Exit(-1)
	}
}

type HandlerFunc func(conn *net.Conn)

func Start(config Config) {

	switch config.Mode {
	case "normal":
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 0, 30, 2, 1
	case "fast":
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 0, 20, 2, 1
	case "fast2":
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 1, 20, 2, 1
	case "fast3":
		config.NoDelay, config.Interval, config.Resend, config.NoCongestion = 1, 10, 2, 1
	}

	pass := pbkdf2.Key([]byte(config.Key), []byte(SALT), 4096, 32, sha1.New)
	var block kcp.BlockCrypt
	switch config.Crypt {
	case "tea":
		block, _ = kcp.NewTEABlockCrypt(pass[:16])
	case "xor":
		block, _ = kcp.NewSimpleXORBlockCrypt(pass)
	case "none":
		block, _ = kcp.NewNoneBlockCrypt(pass)
	case "aes-128":
		block, _ = kcp.NewAESBlockCrypt(pass[:16])
	case "aes-192":
		block, _ = kcp.NewAESBlockCrypt(pass[:24])
	case "blowfish":
		block, _ = kcp.NewBlowfishBlockCrypt(pass)
	case "twofish":
		block, _ = kcp.NewTwofishBlockCrypt(pass)
	case "cast5":
		block, _ = kcp.NewCast5BlockCrypt(pass[:16])
	case "3des":
		block, _ = kcp.NewTripleDESBlockCrypt(pass[:24])
	case "xtea":
		block, _ = kcp.NewXTEABlockCrypt(pass[:16])
	case "salsa20":
		block, _ = kcp.NewSalsa20BlockCrypt(pass)
	default:
		config.Crypt = "aes"
		block, _ = kcp.NewAESBlockCrypt(pass)
	}

	lis, err := kcp.ListenWithOptions(config.Listen, block, config.DataShard, config.ParityShard)
	checkError(err)
	log.Println("listening on:", lis.Addr())
	log.Println("target:", config.Target)
	log.Println("encryption:", config.Crypt)
	log.Println("nodelay parameters:", config.NoDelay, config.Interval, config.Resend, config.NoCongestion)
	log.Println("sndwnd:", config.SndWnd, "rcvwnd:", config.RcvWnd)
	log.Println("compression:", !config.NoComp)
	log.Println("mtu:", config.MTU)
	log.Println("datashard:", config.DataShard, "parityshard:", config.ParityShard)
	log.Println("acknodelay:", config.AckNodelay)
	log.Println("dscp:", config.DSCP)
	log.Println("sockbuf:", config.SockBuf)
	log.Println("keepalive:", config.KeepAlive)

	if err := lis.SetDSCP(config.DSCP); err != nil {
		log.Println("SetDSCP:", err)
	}
	if err := lis.SetReadBuffer(config.SockBuf); err != nil {
		log.Println("SetReadBuffer:", err)
	}
	if err := lis.SetWriteBuffer(config.SockBuf); err != nil {
		log.Println("SetWriteBuffer:", err)
	}
	for {
		if conn, err := lis.AcceptKCP(); err == nil {
			log.Println("remote address:", conn.RemoteAddr())
			conn.SetStreamMode(true)
			conn.SetNoDelay(config.NoDelay, config.Interval, config.Resend, config.NoCongestion)
			conn.SetMtu(config.MTU)
			conn.SetWindowSize(config.SndWnd, config.RcvWnd)
			conn.SetACKNoDelay(config.AckNodelay)
			conn.SetKeepAlive(config.KeepAlive)

			if config.NoComp {
				go handleMux(conn, &config)
			} else {
				go handleMux(newCompStream(conn), &config)
			}
		} else {
			log.Printf("%+v", err)
		}
	}
}
