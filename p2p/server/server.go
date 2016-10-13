package server

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/vzex/dog-tunnel/common"
)

var g_Master net.Listener

func Start(tcpAddr, udpAddr string, useSSL bool, certFile, keyFile string) error {

	common.Conn2ClientInfo = make(map[net.Conn]*common.ClientInfo)
	common.ServerName2Conn = make(map[string]net.Conn)

	listener, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return errors.New("cannot listen addr: " + err.Error())
	}
	if useSSL {
		config := &tls.Config{}
		config.Certificates = make([]tls.Certificate, 1)
		config.Certificates[0], err = tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return errors.New("load key file error: " + err.Error())
		}
		g_Master = tls.NewListener(listener, config)
	} else {
		g_Master = listener
	}
	go func() {
		for {
			conn, err := g_Master.Accept()
			if err != nil {
				continue
			}
			go handleClient(conn)
		}
	}()

	udpaddr, err := net.ResolveUDPAddr("udp", udpAddr)
	if err != nil {
		return errors.New("Can't resolve address: " + err.Error())
	}

	udpconn, err := net.ListenUDP("udp", udpaddr)
	if err != nil {
		return errors.New("Error UDP listening: " + err.Error())
	}

	defer udpconn.Close()

	go udphandleClient(udpconn)

	return nil
}

func Stop() {
	for conn, client := range common.Conn2ClientInfo {
		if !client.IsServer {
			log.Println("shutdown client", client.ServerName)
			common.Write(conn, "0", "showandquit", "server shutdown")
		} else {
			log.Println("unregister service Name", client.ServerName)
			//donnot showandquit,because client_server need to reconnect
		}
	}
}

// ************************

func handleClient(conn net.Conn) {
	common.Conn2ClientInfo[conn] = &common.ClientInfo{Conn: conn, ClientMap: make(map[net.Conn]*common.Session), Id2Session: make(map[string]*common.Session), IsServer: false, Quit: make(chan bool), ResponseTime: time.Now().Unix()}
	log.Println("client linked success", conn.RemoteAddr().String())
	common.Conn2ClientInfo[conn].Loop()
	common.Read(conn, handleResponse)
	client, bHave := common.Conn2ClientInfo[conn]
	if bHave {
		close(client.Quit)
		if client.IsServer {
			for conn, session := range client.ClientMap {
				conn.Close()
				common.RmId(client.ServerName, session.Id)
			}
			delete(common.ServerName2Conn, client.ServerName)
			log.Println("unregister service Name", client.ServerName)
		} else {
			common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
				id := server.DelClient(conn)
				log.Println("send quit")
				common.Write(server.Conn, id, "clientquit", "")
			}, func() {})
		}
		delete(common.Conn2ClientInfo, conn)
	}
	conn.Close()
	log.Println("client disconnected", conn.RemoteAddr().String())
}

func udphandleClient(conn *net.UDPConn) {

	for {

		data := make([]byte, 1024)

		_, remoteAddr, err := conn.ReadFromUDP(data)
		if err != nil {
			log.Println("failed to read UDP msg because of ", err.Error())
			break
		}

		conn.WriteToUDP([]byte(remoteAddr.String()), remoteAddr)
	}
}

func handleResponse(conn net.Conn, id string, action string, content string) {
	log.Println("[got]", id, action, content)
	common.GetClientInfoByConn(conn, func(client *common.ClientInfo) {
		client.ResponseTime = time.Now().Unix()
	}, func() {
	})
	switch action {
	case "init":
		fmt.Println("[init]")
		clientInfoStr := content
		var clientInfo common.ClientSetting
		err := json.Unmarshal([]byte(clientInfoStr), &clientInfo)
		if err != nil {
			log.Println("error decode clientinfo, kick out", conn.RemoteAddr().String())
			common.Write(conn, "0", "showandquit", "server decode clientInfo error")
			return
		}
		if common.Version != clientInfo.Version {
			s_version := fmt.Sprintf("%.2f", common.Version)
			c_version := fmt.Sprintf("%.2f", clientInfo.Version)
			log.Println("version not eq", conn.RemoteAddr().String(), s_version, c_version)
			common.Write(conn, "0", "showandquit", "client version:"+c_version+" not eq with server:"+s_version)
			return
		}
		ServerName := clientInfo.Name
		if clientInfo.ClientType == "reg" {
			common.GetClientInfoByName(ServerName, func(server *common.ClientInfo) {
				common.Write(conn, "0", "showandretry", "already have the ServerName!")
			}, func() {
				common.ServerName2Conn[ServerName] = conn
				common.GetClientInfoByConn(conn, func(info *common.ClientInfo) {
					info.ServerName = ServerName
					info.IsServer = true
					info.Id2MakeSession = make(map[string]*common.UDPMakeSession)
					info.ClientKey = clientInfo.ClientKey
				}, func() {})
				log.Println("client reg service success", conn.RemoteAddr().String(), ServerName)
				common.Write(conn, "0", "show", "[from SRV]register service ok.")
			})
		} else if clientInfo.ClientType == "link" {
			if clientInfo.Mode < 0 || clientInfo.Mode > 2 {
				clientInfo.Mode = 0
			}
			ServerName := clientInfo.Name
			common.GetClientInfoByConn(conn, func(client *common.ClientInfo) {
				client.ServerName = ServerName
			}, func() {
			})
			common.GetClientInfoByName(ServerName, func(server *common.ClientInfo) {
				log.Println("client link service success", conn.RemoteAddr().String(), ServerName)
				server.AddClient(conn, clientInfo)
			}, func() {
				common.Write(conn, "0", "showandquit", "donnt have this service name")
			})
		}
	case "tunnel_error":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			log.Println("<<=====tunnel_error", server.ServerName, conn.RemoteAddr().String())
			session, bHave := server.Id2Session[id]
			if bHave {
				session.Status = "fail"
				common.Write(session.ClientA, "0", "showandquit", content)
				server.DelClient(session.ClientA)
			}
		}, func() {
		})
	case "makeholefail":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			udpsession, bHave := server.Id2MakeSession[id]
			if bHave {
				log.Println("<<=====make hole fail", conn.RemoteAddr().String(), udpsession.ServerName, udpsession.SessionId, id)
				sessionId := udpsession.SessionId
				session, _bHave := server.Id2Session[sessionId]
				if _bHave {
					session.Status = "fail"
					session.MakeHoleResponseN++
					session.MakeHoleHasFail = true
					if session.MakeHoleResponseN == session.Setting.PipeNum {
						if session.Method == "udp" {
							session.RestartSession(server.ServerName)
						} else if session.Method == "restart" {
							if session.Setting.Mode == 0 {
								tmp := session.ClientA
								session.ClientA = session.ClientB
								session.ClientB = tmp
								session.StartCSMode()
							} else {
								server.DelClient(session.ClientB)
							}
						} else {
							server.DelClient(session.ClientA)
						}
					}
				}
				udpsession.Remove(false)
			}
		}, func() {
		})
	case "makeholeok":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			if content == "csmode" {
				session, _bHave := server.Id2Session[id]
				if _bHave {
					log.Println("<<=====make hole ok", conn.RemoteAddr().String(), server.ServerName, session.Id)
					session.Status = "ok"
					session.MakeHoleResponseN++
				}
			}
			udpsession, bHave := server.Id2MakeSession[id]
			if bHave {
				log.Println("<<=====make hole ok", conn.RemoteAddr().String(), udpsession.ServerName, udpsession.SessionId, id)
				sessionId := udpsession.SessionId
				session, _bHave := server.Id2Session[sessionId]
				if _bHave {
					session.MakeHoleResponseN++
					if session.MakeHoleResponseN == session.Setting.PipeNum {
						if !session.MakeHoleHasFail {
							session.Status = "ok"
						}
					}
				}
				udpsession.Remove(false)
			}
		}, func() {
		})
	case "report_addrlist":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			udpsession, bHave := server.Id2MakeSession[id]
			//log.Println("test", udpsession, id, server.ServerName)
			if bHave {
				log.Println("<<===report addr list ok", conn.RemoteAddr().String(), udpsession.ServerName, udpsession.Id)
				udpsession.BeginMakeHole(1, content)
			}
		}, func() {
		})
	case "success_bust_a":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			udpsession, bHave := server.Id2MakeSession[id]
			if bHave {
				log.Println("<<=====success_bust_a", conn.RemoteAddr().String(), udpsession.ServerName, udpsession.SessionId, id)
				udpsession.BeginMakeHole(2, content)
			}
		}, func() {
		})
	// for c/s mode
	case "tunnel_close":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			session := server.GetSession(conn)
			if session != nil {
				common.Write(session.ClientB, session.Id+"-"+id, "csmode_s_tunnel_close", content)
			} else {
				println("no session")
			}
		}, func() {
		})
	case "tunnel_open":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			session := server.GetSession(conn)
			if session != nil {
				common.Write(session.ClientB, session.Id+"-"+id, "csmode_s_tunnel_open", content)
			} else {
				println("no session")
			}
		}, func() {
		})
	case "tunnel_msg_c":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			common.Write(conn, "0", "showandquit", "cannot get userinfo of this service "+server.UserName)
			return
		}, func() {
		})
	case "tunnel_msg_s":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			common.Write(conn, "0", "showandquit", "cannot get userinfo of this service"+server.UserName)
			return
		}, func() {
		})
	case "tunnel_close_s":
		common.GetServerInfoByConn(conn, func(server *common.ClientInfo) {
			arr := strings.Split(id, "-")
			clientId := arr[0]
			session, bHave := server.Id2Session[clientId]
			if bHave {
				common.Write(session.ClientA, id, "csmode_c_tunnel_close", content)
			} else {
				println("no session")
			}
		}, func() {
		})
	}
}
