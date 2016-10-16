package client

import (
	"crypto/sha1"
	"io"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/pbkdf2"

	"github.com/golang/snappy"
	"github.com/pkg/errors"
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

	addr, err := net.ResolveTCPAddr("tcp", config.LocalAddr)
	checkError(err)
	listener, err := net.ListenTCP("tcp", addr)
	checkError(err)

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

	log.Println("listening on:", listener.Addr())
	log.Println("encryption:", config.Crypt)
	log.Println("nodelay parameters:", config.NoDelay, config.Interval, config.Resend, config.NoCongestion)
	log.Println("remote address:", config.RemoteAddr)
	log.Println("sndwnd:", config.SndWnd, "rcvwnd:", config.RcvWnd)
	log.Println("compression:", !config.NoComp)
	log.Println("mtu:", config.MTU)
	log.Println("datashard:", config.DataShard, "parityshard:", config.ParityShard)
	log.Println("acknodelay:", config.AckNodelay)
	log.Println("dscp:", config.DSCP)
	log.Println("sockbuf:", config.SockBuf)
	log.Println("keepalive:", config.KeepAlive)
	log.Println("conn:", config.Conn)
	log.Println("autoexpire:", config.AutoExpire)

	smuxConfig := smux.DefaultConfig()
	smuxConfig.MaxReceiveBuffer = config.SockBuf

	createConn := func() (*smux.Session, error) {
		kcpconn, err := kcp.DialWithOptions(config.RemoteAddr, block, config.DataShard, config.ParityShard)
		if err != nil {
			return nil, errors.Wrap(err, "createConn()")
		}
		kcpconn.SetStreamMode(true)
		kcpconn.SetNoDelay(config.NoDelay, config.Interval, config.Resend, config.NoCongestion)
		kcpconn.SetWindowSize(config.SndWnd, config.RcvWnd)
		kcpconn.SetMtu(config.MTU)
		kcpconn.SetACKNoDelay(config.AckNodelay)
		kcpconn.SetKeepAlive(config.KeepAlive)

		if err := kcpconn.SetDSCP(config.DSCP); err != nil {
			log.Println("SetDSCP:", err)
		}
		if err := kcpconn.SetReadBuffer(config.SockBuf); err != nil {
			log.Println("SetReadBuffer:", err)
		}
		if err := kcpconn.SetWriteBuffer(config.SockBuf); err != nil {
			log.Println("SetWriteBuffer:", err)
		}

		// stream multiplex
		var session *smux.Session
		if config.NoComp {
			session, err = smux.Client(kcpconn, smuxConfig)
		} else {
			session, err = smux.Client(newCompStream(kcpconn), smuxConfig)
		}
		if err != nil {
			return nil, errors.Wrap(err, "createConn()")
		}
		return session, nil
	}

	// wait until a connection is ready
	waitConn := func() *smux.Session {
		for {
			if session, err := createConn(); err == nil {
				return session
			} else {
				time.Sleep(time.Second)
			}
		}
	}

	numconn := uint16(config.Conn)
	muxes := make([]struct {
		session *smux.Session
		ttl     time.Time
	}, numconn)

	for k := range muxes {
		sess, err := createConn()
		checkError(err)
		muxes[k].session = sess
		muxes[k].ttl = time.Now().Add(time.Duration(config.AutoExpire) * time.Second)
	}

	chScavenger := make(chan *smux.Session, 128)
	go scavenger(chScavenger)
	rr := uint16(0)
	for {
		p1, err := listener.AcceptTCP()
		if err := p1.SetReadBuffer(config.SockBuf); err != nil {
			log.Println("TCP SetReadBuffer:", err)
		}
		if err := p1.SetWriteBuffer(config.SockBuf); err != nil {
			log.Println("TCP SetWriteBuffer:", err)
		}
		checkError(err)
		idx := rr % numconn

	OPEN_P2:
		// do auto expiration
		if config.AutoExpire > 0 && time.Now().After(muxes[idx].ttl) {
			chScavenger <- muxes[idx].session
			muxes[idx].session = waitConn()
			muxes[idx].ttl = time.Now().Add(time.Duration(config.AutoExpire) * time.Second)
		}

		// do session open
		p2, err := muxes[idx].session.OpenStream()
		if err != nil { // mux failure
			chScavenger <- muxes[idx].session
			muxes[idx].session = waitConn()
			muxes[idx].ttl = time.Now().Add(time.Duration(config.AutoExpire) * time.Second)
			goto OPEN_P2
		}
		go handleClient(p1, p2)
		rr++
	}

}

type scavengeSession struct {
	session *smux.Session
	ttl     time.Time
}

const (
	maxScavengeTTL = 10 * time.Minute
)

func scavenger(ch chan *smux.Session) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var sessionList []scavengeSession
	for {
		select {
		case sess := <-ch:
			sessionList = append(sessionList, scavengeSession{sess, time.Now()})
		case <-ticker.C:
			var newList []scavengeSession
			for k := range sessionList {
				s := sessionList[k]
				if s.session.NumStreams() == 0 || s.session.IsClosed() || time.Since(s.ttl) > maxScavengeTTL {
					log.Println("session scavenged")
					s.session.Close()
				} else {
					newList = append(newList, sessionList[k])
				}
			}
			sessionList = newList
		}
	}
}
