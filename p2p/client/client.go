package client

import (
	"bufio"
	"crypto/cipher"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/kofj/dog-tunnel/common"
	"github.com/kofj/dog-tunnel/nat"
)

type Setting struct {
	id         string
	addr       string // addr for listen or connect(value \"socks5\" means tcp socks5 proxy for reg),depends on link or reg
	clientType string // reg or link

	BusterAddr string // MakeHole server
	RemoteAddr string // connect remote server
	PipeNum    int    // pipe num for transmission
	ClientKey  string // when other client linkt to the reg client, need clientkey, or empty
	ClientMode int    // connect mode:0 if p2p fail, use c/s mode;1 just p2p mode;2 just c/s mode
	UseSSL     bool   // use ssl
	Verbose    bool   // verbose mode
	InitAddr   string // addip for bust,xx.xx.xx.xx;xx.xx.xx.xx;
}

var (
	DnsCacheNum int = 0 // if > 0, dns will cache xx minutes
)

type P2pClient struct {
	setting *Setting

	g_ClientMap     map[string]*Client
	g_ClientMapKey  map[string]*cipher.Block
	g_Id2UDPSession map[string]*UDPMakeSession
	remoteConn      net.Conn

	mode   string
	aesKey *cipher.Block

	Receive func(msg []byte)
	Reply   func() (msg []byte)
	Send    func(func(msg []byte) error)
}

func init() {
	checkDns = make(chan *dnsQueryReq)
	checkDnsRes = make(chan *dnsQueryBack)
	go dnsLoop()
}

func New(s Setting) P2pClient {
	return P2pClient{
		g_ClientMap:     make(map[string]*Client),
		g_ClientMapKey:  make(map[string]*cipher.Block),
		g_Id2UDPSession: make(map[string]*UDPMakeSession),
		setting:         &s,
		Receive:         func(msg []byte) { println("Recv MSG from remote") },
		Reply:           func() (msg []byte) { println("Reply MSG to remote"); return []byte("小尾巴～") },
		Send:            func(func(msg []byte) error) { println("Send MSG to remote") },
	}
}

func (p *P2pClient) Reg(id string) {
	p.setting.id = id
	p.setting.clientType = "reg"
	p.start()
}

func (p *P2pClient) Link(id string) {
	p.setting.id = id
	p.setting.clientType = "link"
	p.start()
}

func (p *P2pClient) start() {
	//var err error
	if p.setting.UseSSL {
		_remoteConn, err := tls.Dial("tcp", p.setting.RemoteAddr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			println("connect remote err:" + err.Error())
			return
		}
		p.remoteConn = net.Conn(_remoteConn)
	} else {
		_remoteConn, err := net.DialTimeout("tcp", p.setting.RemoteAddr, 10*time.Second)
		if err != nil {
			println("connect remote err:" + err.Error())
			return
		}
		p.remoteConn = _remoteConn
	}
	println("connect to server succeed")
	go p.connect()
	q := make(chan bool)
	go func() {
		c := time.NewTicker(time.Second * 10)
	out:
		for {
			select {
			case <-c.C:
				if p.remoteConn != nil {
					common.Write(p.remoteConn, "-1", "ping", "")
				}
			case <-q:
				break out
			}
		}
		c.Stop()
	}()

	common.Read(p.remoteConn, p.handleResponse)
	q <- true
	for clientId, client := range p.g_ClientMap {
		log.Println("client shutdown", clientId)
		client.Quit()
	}

	for _, session := range p.g_Id2UDPSession {
		if session.engine != nil {
			session.engine.Fail()
		}
	}
	if p.remoteConn != nil {
		p.remoteConn.Close()
	}
}

func (p *P2pClient) connect() {
	if p.setting.PipeNum < 1 {
		p.setting.PipeNum = 1
	}
	clientInfo := common.ClientSetting{
		Version:    common.Version,
		Mode:       p.setting.ClientMode,
		PipeNum:    p.setting.PipeNum,
		ClientKey:  p.setting.ClientKey,
		ClientType: p.setting.clientType,
		Name:       p.setting.id,
	}

	clientInfoStr, _ := json.Marshal(clientInfo)
	log.Println("[init client]", string(clientInfoStr),
		common.Write(p.remoteConn, "0", "init", string(clientInfoStr)),
	)
}

func (p *P2pClient) disconnect() {
	if p.remoteConn != nil {
		p.remoteConn.Close()
		p.remoteConn = nil
	}
}

func (p *P2pClient) handleResponse(conn net.Conn, clientId string, action string, content string) {
	// log.Println("[client.handleResponse]got", clientId, action, content)
	switch action {
	case "show":
		fmt.Println(time.Now().Format("2006-01-02 15:04:05"), content)
	case "showandretry":
		fmt.Println(time.Now().Format("2006-01-02 15:04:05"), content)
		p.remoteConn.Close()
	case "showandquit":
		fmt.Println(time.Now().Format("2006-01-02 15:04:05"), content)
		p.remoteConn.Close()
	case "clientquit":
		client := p.g_ClientMap[clientId]
		log.Println("clientquit!!!", clientId, client)
		if client != nil {
			client.Quit()
		}
	case "remove_udpsession":
		log.Println("server force remove udpsession", clientId)
		delete(p.g_Id2UDPSession, clientId)
	case "query_addrlist_a":
		outip := content
		arr := strings.Split(clientId, "-")
		id := arr[0]
		sessionId := arr[1]
		pipeType := arr[2]
		p.g_Id2UDPSession[id] = &UDPMakeSession{id: id, p2p: p, sessionId: sessionId, pipeType: pipeType}
		go p.g_Id2UDPSession[id].reportAddrList(true, outip)
	case "query_addrlist_b":
		arr := strings.Split(clientId, "-")
		id := arr[0]
		sessionId := arr[1]
		pipeType := arr[2]
		p.g_Id2UDPSession[id] = &UDPMakeSession{id: id, p2p: p, sessionId: sessionId, pipeType: pipeType}
		go p.g_Id2UDPSession[id].reportAddrList(false, content)
	case "tell_bust_a":
		session, bHave := p.g_Id2UDPSession[clientId]
		if bHave {
			go session.beginMakeHole(content)
		}
	case "tell_bust_b":
		session, bHave := p.g_Id2UDPSession[clientId]
		if bHave {
			go session.beginMakeHole("")
		}
	case "csmode_c_tunnel_close":
		log.Println("receive close msg from server")
		arr := strings.Split(clientId, "-")
		clientId = arr[0]
		sessionId := arr[1]
		client, bHave := p.g_ClientMap[clientId]
		if bHave {
			client.removeSession(sessionId)
		}
	case "csmode_s_tunnel_close":
		arr := strings.Split(clientId, "-")
		clientId = arr[0]
		sessionId := arr[1]
		client, bHave := p.g_ClientMap[clientId]
		if bHave {
			client.removeSession(sessionId)
		}
	case "csmode_s_tunnel_open":
		oriId := clientId
		arr := strings.Split(oriId, "-")
		clientId = arr[0]
		sessionId := arr[1]
		client, bHave := p.g_ClientMap[clientId]
		if !bHave {
			client = &Client{id: clientId, p2p: p, pipes: make(map[int]net.Conn), engine: nil, buster: true, sessions: make(map[string]*clientSession), ready: true, bUdp: false}
			client.pipes[0] = p.remoteConn
			p.g_ClientMap[clientId] = client
		} else {
			client.pipes[0] = p.remoteConn
			client.ready = true
			client.bUdp = false
		}

		client.sessionLock.Lock()
		client.sessions[sessionId] = &clientSession{pipe: p.remoteConn}
		client.sessionLock.Unlock()
		go handleLocalPortResponse(client, oriId)

	case "csmode_c_begin":
		client, bHave := p.g_ClientMap[clientId]
		if !bHave {
			client = &Client{id: clientId, p2p: p, pipes: make(map[int]net.Conn), engine: nil, buster: false, sessions: make(map[string]*clientSession), ready: true, bUdp: false}
			client.pipes[0] = p.remoteConn
			p.g_ClientMap[clientId] = client
		} else {
			client.pipes[0] = p.remoteConn
			client.ready = true
			client.bUdp = false
		}
		if client.ClientBegin() {
			common.Write(p.remoteConn, clientId, "makeholeok", "csmode")
		}
	case "csmode_msg_c":
		arr := strings.Split(clientId, "-")
		clientId = arr[0]
		_, bHave := p.g_ClientMap[clientId]
		if bHave {
			p.Receive([]byte(content))
		}
	case "csmode_msg_s":
		arr := strings.Split(clientId, "-")
		clientId = arr[0]
		_, bHave := p.g_ClientMap[clientId]
		if bHave {
			p.Receive([]byte(content))
		}
	}

}

// ***************************************

type Client struct {
	id          string
	p2p         *P2pClient
	buster      bool
	engine      *nat.AttemptEngine
	pipes       map[int]net.Conn          // client for pipes
	specPipes   map[string]net.Conn       // client for pipes
	sessions    map[string]*clientSession // session to pipeid
	sessionLock sync.RWMutex
	ready       bool
	bUdp        bool
}

// pipe : client to client
// local : client to local apps
func (sc *Client) getSession(sessionId string) *clientSession {
	sc.sessionLock.RLock()
	session, _ := sc.sessions[sessionId]
	sc.sessionLock.RUnlock()
	return session
}

func (sc *Client) removeSession(sessionId string) bool {
	if sc.p2p.setting.clientType == "link" {
		common.RmId("udp", sessionId)
	}
	sc.sessionLock.RLock()
	session, bHave := sc.sessions[sessionId]
	sc.sessionLock.RUnlock()
	if bHave {
		if session.localConn != nil {
			session.localConn.Close()
		}
		sc.sessionLock.Lock()
		delete(sc.sessions, sessionId)
		sc.sessionLock.Unlock()
		//log.Println("client", sc.id, "remove session", sessionId)
		return true
	}
	return false
}

func (sc *Client) OnTunnelRecv(pipe net.Conn, sessionId string, action string, content string) {
	session := sc.getSession(sessionId)
	var conn net.Conn
	if session != nil {
		conn = session.localConn
	}
	switch action {
	case "tunnel_error":
		if conn != nil {
			conn.Write([]byte(content))
			log.Println("tunnel error", content, sessionId)
		}
		sc.removeSession(sessionId)
		//case "serve_begin":
	case "tunnel_msg_s":
		// client recive
		go sc.p2p.Receive([]byte(content))
	case "tunnel_close_s":
		sc.removeSession(sessionId)
	case "ping", "pingback":
		//log.Println("recv", action)
		if action == "ping" {
			common.Write(pipe, sessionId, "pingback", "")
		}
	case "tunnel_msg_c":
		if conn != nil {
			//log.Println("tunnel", len(content), sessionId)
			conn.Write([]byte(content))
		}
	case "tunnel_close":
		sc.removeSession(sessionId)
	case "tunnel_open":
		if sc.p2p.setting.clientType == "reg" {
			sc.sessionLock.Lock()
			sc.sessions[sessionId] = &clientSession{pipe: pipe}
			sc.sessionLock.Unlock()
			go handleLocalPortResponse(sc, sessionId)
		}
	}
}

func (sc *Client) Quit() {
	log.Println("client quit", sc.id)
	if _, ok := sc.p2p.g_ClientMap[sc.id]; ok {
		delete(sc.p2p.g_ClientMap, sc.id)
	}
	if _, ok := sc.p2p.g_ClientMapKey[sc.id]; ok {
		delete(sc.p2p.g_ClientMapKey, sc.id)
	}
	for id, _ := range sc.sessions {
		sc.removeSession(id)
	}
	for _, pipe := range sc.pipes {
		if pipe != sc.p2p.remoteConn {
			pipe.Close()
		}
	}
	if sc.engine != nil {
		sc.engine.Fail()
	}
}

func (sc *Client) ClientBegin() bool {
	write := func(msg []byte) (err error) {
		pipe := sc.getOnePipe()
		if pipe == nil {
			log.Println("cannot get pipe for client")
			err = errors.New("cannot get pipe for client")
			return
		}
		sessionId := common.GetId("udp")
		common.Write(pipe, sessionId, "tunnel_open", "")
		err = common.Write(pipe, sessionId, "tunnel_msg_c", string(msg))
		// common.Write(pipe, sessionId, "tunnel_close", "")
		return
	}
	go sc.p2p.Send(write)

	sc.p2p.mode = "p2p"
	if !sc.bUdp {
		sc.p2p.mode = "c/s"
		delete(sc.p2p.g_ClientMapKey, sc.id)
	}

	return true
}

func (sc *Client) getOnePipe() net.Conn {
	tmp := []int{}
	for id, _ := range sc.pipes {
		tmp = append(tmp, id)
	}
	size := len(tmp)
	if size == 0 {
		return nil
	}
	index := rand.Intn(size)
	log.Println("choose pipe for ", sc.id, ",", index, "of", size)
	hitId := tmp[index]
	pipe, _ := sc.pipes[hitId]
	return pipe
}

func (sc *Client) Run(index int, specPipe string) {
	println("[Client][Run]index:", index)
	var pipe net.Conn
	if index >= 0 {
		pipe = sc.pipes[index]
	} else {
		pipe = sc.specPipes[specPipe]
	}
	if pipe == nil {
		return
	}
	go func() {
		callback := func(conn net.Conn, sessionId, action, content string) {
			if sc != nil {
				sc.OnTunnelRecv(conn, sessionId, action, content)
			}
		}
		common.Read(pipe, callback)
		println("client end read", index)
		if index >= 0 {
			delete(sc.pipes, index)
			if sc.p2p.setting.clientType == "link" {
				if len(sc.pipes) == 0 {
					if sc.p2p.remoteConn != nil {
						sc.p2p.remoteConn.Close()
					}
				}
			}
		} else {
			delete(sc.specPipes, specPipe)
		}
	}()
}

func (sc *Client) LocalAddr() net.Addr                { return nil }
func (sc *Client) Close() error                       { return nil }
func (sc *Client) RemoteAddr() net.Addr               { return nil }
func (sc *Client) SetDeadline(t time.Time) error      { return nil }
func (sc *Client) SetReadDeadline(t time.Time) error  { return nil }
func (sc *Client) SetWriteDeadline(t time.Time) error { return nil }

func handleLocalPortResponse(client *Client, id string) {
	msg := client.p2p.Reply()
	pipe := client.getOnePipe()
	common.Write(pipe, id, "tunnel_msg_s", string(msg))
	common.Write(pipe, id, "tunnel_close_s", "")
}

// ***************************************
var (
	DefaultSettings = Setting{
		id:         "mac",
		clientType: "reg",

		BusterAddr: "10.200.1.100:16006",
		RemoteAddr: "10.200.1.100:16005",
		// BusterAddr: "188.166.248.38:8018",
		// RemoteAddr: "188.166.248.38:8019",
		PipeNum:    1,
		ClientKey:  "p2p",
		ClientMode: 0,
		UseSSL:     false,
		Verbose:    true,
		InitAddr:   "127.0.0.1",
	}
	// Settings = Setting{
	// 	BusterAddr: "dog-tunnel.tk:8018",
	// 	RemoteAddr: "dog-tunnel.tk:8000",
	// 	PipeNum:    1,
	// 	ClientKey:  "p2p",
	// 	ClientMode: 0,
	// 	UseSSL:     true,
	// 	Verbose:    false,
	// }
)

// version
func Version() string {
	return fmt.Sprintf("%.2f\n", common.Version)
}
