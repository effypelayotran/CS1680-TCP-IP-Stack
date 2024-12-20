package tcp

import (
	"fmt"
	"ipp/pkg/protocol"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
)

var tcp *TCPLayer

func Init(ipStack *protocol.IPStack) {
	tcp = &TCPLayer{
		SocketTable:    make(map[VTCPConn]*TCPSocket),
		ListenersTable: make(map[uint16]*VTCPListener),
		UsedPorts:      make(map[uint16]bool),
		LocalIP:        ipStack.LinkLayer.Interfaces[0].AssignedIP,
		IPStack:        *ipStack,
	}
}

func TcpHandler(data []byte, params []interface{}) {
	packetHeader := params[0].(*ipv4header.IPv4Header)
	srcIP := packetHeader.Src
	tcpPacket := UnmarshalTCPPacket(data, srcIP)

	tcpConn := VTCPConn{
		LocalIP:    packetHeader.Dst,
		LocalPort:  tcpPacket.Header.DstPort,
		RemoteIP:   srcIP,
		RemotePort: tcpPacket.Header.SrcPort,
	}

	if tcpConn.LocalIP != tcp.LocalIP {
		fmt.Println("Dest ip is not our ip", packetHeader.Dst.String())
		return
	}

	tcpSocket, found := tcp.SocketTable[tcpConn]
	if !found {
		tcp.Lock.Lock()
		listener, ok := tcp.ListenersTable[tcpConn.LocalPort]
		tcp.Lock.Unlock()

		if !ok {
			fmt.Println("No listener on port ", tcpConn.LocalPort)
			return
		}
		listener.PacketChannel <- tcpPacket
		
	} else {
		tcpSocket.PacketChannel <- tcpPacket
	}
}

func PrintSockets() {
	tcp.Lock.Lock()
	defer tcp.Lock.Unlock()
	res := "SID      LAddr LPort      RAddr RPort       Status\n"
	res += "--------------------------------------------------\n"
	for port, listener := range tcp.ListenersTable {
		res += fmt.Sprintf("%d    0.0.0.0 %d    0.0.0.0 0       LISTEN\n", listener.SocketID, port)
	}

	for conn, sock := range tcp.SocketTable {
		res += fmt.Sprintf("%d      ", sock.SocketID)
		res += fmt.Sprintf("%s ", conn.LocalIP)
		res += fmt.Sprintf("%d      ", conn.LocalPort)
		res += fmt.Sprintf("%s ", conn.RemoteIP)
		res += fmt.Sprintf("%d       ", conn.RemotePort)
		res += fmt.Sprintf("%s\n", ConvertStateToString(sock.ConnState))
	}

	fmt.Println(res)
}
