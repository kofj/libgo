package client

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/kofj/dog-tunnel/common"
	"github.com/kofj/dog-tunnel/nat"
)

// UDP Make session

type UDPMakeSession struct {
	id        string
	p2p       *P2pClient
	sessionId string
	buster    bool
	engine    *nat.AttemptEngine
	delay     int
	pipeType  string
}

func (session *UDPMakeSession) beginMakeHole(content string) {
	engine := session.engine
	if engine == nil {
		return
	}
	addrList := content
	if session.buster {
		engine.SetOtherAddrList(addrList)
	}
	log.Println("begin bust", session.id, session.sessionId, session.buster)
	if session.p2p.setting.clientType == "link" && !session.buster {
		log.Println("retry bust!")
	}
	report := func() {
		if session.buster {
			if session.delay > 0 {
				log.Println("try to delay", session.delay, "seconds")
				time.Sleep(time.Duration(session.delay) * time.Second)
			}
			go common.Write(session.p2p.remoteConn, session.id, "success_bust_a", "")
		}
	}
	oldSession := session
	var aesBlock *cipher.Block
	if session.p2p.setting.clientType == "link" {
		aesBlock = session.p2p.aesKey
	} else {
		aesBlock, _ = session.p2p.g_ClientMapKey[session.sessionId]
	}
	var conn net.Conn
	var err error
	if aesBlock == nil {
		conn, err = engine.GetConn(report, nil, nil)
	} else {
		conn, err = engine.GetConn(report, func(s []byte) []byte {
			if aesBlock == nil {
				return s
			} else {
				padLen := aes.BlockSize - (len(s) % aes.BlockSize)
				for i := 0; i < padLen; i++ {
					s = append(s, byte(padLen))
				}
				srcLen := len(s)
				encryptText := make([]byte, srcLen+aes.BlockSize)
				iv := encryptText[srcLen:]
				for i := 0; i < len(iv); i++ {
					iv[i] = byte(i)
				}
				mode := cipher.NewCBCEncrypter(*aesBlock, iv)
				mode.CryptBlocks(encryptText[:srcLen], s)
				return encryptText
			}
		}, func(s []byte) []byte {
			if aesBlock == nil {
				return s
			} else {
				if len(s) < aes.BlockSize*2 || len(s)%aes.BlockSize != 0 {
					return []byte{}
				}
				srcLen := len(s) - aes.BlockSize
				decryptText := make([]byte, srcLen)
				iv := s[srcLen:]
				mode := cipher.NewCBCDecrypter(*aesBlock, iv)
				mode.CryptBlocks(decryptText, s[:srcLen])
				paddingLen := int(decryptText[srcLen-1])
				if paddingLen > 16 {
					return []byte{}
				}
				return decryptText[:srcLen-paddingLen]
			}
		})
	}
	session, _bHave := session.p2p.g_Id2UDPSession[session.id]
	if session != oldSession {
		return
	}
	if !_bHave {
		return
	}
	delete(session.p2p.g_Id2UDPSession, session.id)
	if err == nil {
		if !session.buster {
			common.Write(session.p2p.remoteConn, session.id, "makeholeok", "")
		}
		client, bHave := session.p2p.g_ClientMap[session.sessionId]
		if !bHave {
			client = &Client{id: session.sessionId, p2p: session.p2p, engine: session.engine, buster: session.buster, ready: true, bUdp: true, sessions: make(map[string]*clientSession), specPipes: make(map[string]net.Conn), pipes: make(map[int]net.Conn)}
			session.p2p.g_ClientMap[session.sessionId] = client
		}
		if isCommonSessionId(session.pipeType) {
			size := len(client.pipes)
			client.pipes[size] = conn
			go client.Run(size, "")
			log.Println("[beginMakeHole] add common session", session.buster, session.sessionId, session.id)
			if session.p2p.setting.clientType == "link" {
				if len(client.pipes) == session.p2p.setting.PipeNum {
					client.MultiListen()
				}
			}
		} else {
			client.specPipes[session.pipeType] = conn
			go client.Run(-1, session.pipeType)
			log.Println("add session for", session.pipeType)
		}
	} else {
		delete(session.p2p.g_ClientMap, session.sessionId)
		delete(session.p2p.g_ClientMapKey, session.sessionId)
		log.Println("cannot connect", err.Error())
		if !session.buster && err.Error() != "quit" {
			common.Write(session.p2p.remoteConn, session.id, "makeholefail", "")
		}
	}
}

func (session *UDPMakeSession) reportAddrList(buster bool, outip string) {
	id := session.id
	var otherAddrList string
	if !buster {
		arr := strings.SplitN(outip, ":", 2)
		outip, otherAddrList = arr[0], arr[1]
	} else {
		arr := strings.SplitN(outip, ":", 2)
		var delayTime string
		outip, delayTime = arr[0], arr[1]
		session.delay, _ = strconv.Atoi(delayTime)
		if session.delay < 0 {
			session.delay = 0
		}
	}
	outip += ";" + session.p2p.setting.InitAddr
	_id, _ := strconv.Atoi(id)
	engine, err := nat.Init(outip, buster, _id, session.p2p.setting.BusterAddr)
	if err != nil {
		println("init error", err.Error())
		session.p2p.disconnect()
		return
	}
	session.engine = engine
	session.buster = buster
	if !buster {
		engine.SetOtherAddrList(otherAddrList)
	}
	addrList := engine.GetAddrList()
	println("[reportAddrList]addrList", addrList)
	common.Write(session.p2p.remoteConn, id, "report_addrlist", addrList)
}

// Client Session

type clientSession struct {
	pipe      net.Conn
	localConn net.Conn
	status    string
	recvMsg   string
	extra     uint8
	setting   Setting
}

func (session *clientSession) processSockProxy(sc *Client, sessionId, content string, callback func()) {
	pipe := session.pipe
	session.recvMsg += content
	bytes := []byte(session.recvMsg)
	size := len(bytes)
	// log.Println("recv msg-====", len(session.recvMsg), session.recvMsg, session.status, sessionId)
	switch session.status {
	case "init":
		if session.localConn != nil {
			session.localConn.Close()
			session.localConn = nil
		}
		if size < 2 {
			//println("wait init")
			return
		}
		var _, nmethod uint8 = bytes[0], bytes[1]
		//println("version", version, nmethod)
		session.status = "version"
		session.recvMsg = string(bytes[2:])
		session.extra = nmethod
	case "version":
		if uint8(size) < session.extra {
			//println("wait version")
			return
		}
		var send = []uint8{5, 0}
		go common.Write(pipe, sessionId, "tunnel_msg_s", string(send))
		session.status = "hello"
		session.recvMsg = string(bytes[session.extra:])
		session.extra = 0
		//log.Println("now", len(session.recvMsg))
	case "hello":
		var hello reqMsg
		bOk, tail := hello.read(bytes)
		if bOk {
			go func() {
				var ansmsg ansMsg
				url := hello.url
				var s_conn net.Conn
				var err error
				if DnsCacheNum > 0 && hello.atyp == 3 {
					host := string(hello.dst_addr[1 : 1+hello.dst_addr[0]])
					resChan := make(chan *dnsQueryRes)
					checkDns <- &dnsQueryReq{c: resChan, host: host, port: int(hello.dst_port2), reqtype: hello.reqtype, url: url}
					res := <-resChan
					s_conn = res.conn
					err = res.err
					if res.ip != "" {
						url = net.JoinHostPort(res.ip, fmt.Sprintf("%d", hello.dst_port2))
					}
				}
				if s_conn == nil && err == nil {
					s_conn, err = net.DialTimeout(hello.reqtype, url, 30*time.Second)
				}
				if err != nil {
					log.Println("connect to local server fail:", err.Error())
					ansmsg.gen(&hello, 4)
					go common.Write(pipe, sessionId, "tunnel_msg_s", string(ansmsg.buf[:ansmsg.mlen]))
					return
				} else {
					session.localConn = s_conn
					go handleLocalPortResponse(sc, sessionId)
					ansmsg.gen(&hello, 0)
					go common.Write(pipe, sessionId, "tunnel_msg_s", string(ansmsg.buf[:ansmsg.mlen]))
					session.status = "ok"
					session.recvMsg = string(tail)
					callback()
					return
				}
			}()
		} else {
			//log.Println("wait hello")
		}
		return
	case "ok":
		return
	}
	session.processSockProxy(sc, sessionId, "", callback)
}

func isCommonSessionId(id string) bool {
	return id == "common"
}
