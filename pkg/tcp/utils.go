package tcp

import (
	"fmt"
	"math/rand"
	"net/netip"

	"github.com/google/netstack/tcpip/header"
)

// might not work, we are not checking if port is already a listening port
func getUnusedPort() uint16 {
	port := uint16(1024 + rand.Uint32()%(1<<16-1024))

	_, found := tcp.UsedPorts[port]
	for found {
		port = uint16(1024 + rand.Uint32()%(1<<16-1024))
		_, found = tcp.UsedPorts[port]
	}
	return port
}

func UnmarshalHeader(b []byte) header.TCPFields {
	td := header.TCP(b)
	return header.TCPFields{
		SrcPort:    td.SourcePort(),
		DstPort:    td.DestinationPort(),
		SeqNum:     td.SequenceNumber(),
		AckNum:     td.AckNumber(),
		DataOffset: td.DataOffset(),
		Flags:      td.Flags(),
		WindowSize: td.WindowSize(),
		Checksum:   td.Checksum(),
	}
}

func UnmarshalTCPPacket(rawMsg []byte, srcIP netip.Addr) *TCPPacket {
	tcpHeader := UnmarshalHeader(rawMsg)
	tcpPacket := TCPPacket{
		SrcIP:  srcIP,
		Header: tcpHeader,
		Data:   rawMsg[tcpHeader.DataOffset:],
	}
	return &tcpPacket
}

func (p *TCPPacket) Marshal() []byte {
	tcphdr := make(header.TCP, TCPHeaderLen)
	tcphdr.Encode(&p.Header)

	return append([]byte(tcphdr), p.Data...)
}

func getNextSocketID() uint32 {
	socketIDMutex.Lock()
	defer socketIDMutex.Unlock()
	socketID := socketIDCounter
	socketIDCounter++
	return socketID
}

func GetSocketById(id int) *TCPSocket {
	tcp.Lock.Lock()
	defer tcp.Lock.Unlock()

	for _, sock := range tcp.SocketTable {
		if int(sock.SocketID) == id {
			return sock
		}
	}
	return nil
}

func GetListenerBySocketId(id int) *VTCPListener {
	tcp.Lock.Lock()
	defer tcp.Lock.Unlock()
	fmt.Println("scrolling through listeners table")

	for _, tcpListener := range tcp.ListenersTable {
		fmt.Printf("id %d, listener id %d\n", id, tcpListener.SocketID )
		if tcpListener.SocketID == uint32(id) {
			return tcpListener
		}
	}
	return nil
}

func GetSocketByConn(conn *VTCPConn) *TCPSocket {
	tcp.Lock.Lock()
	socket, found := tcp.SocketTable[*conn]
	tcp.Lock.Unlock()

	if !found {
		return nil
	} else {
		return socket
	}
}

func ConvertStateToString(connState TCPState) string {
	switch connState {
	case CLOSED:
		return "CLOSED"
	case LISTEN:
		return "LISTEN"
	case SYN_SENT:
		return "SYN_SENT"
	case SYN_RECEIVED:
		return "SYN_RECEIVED"
	case ESTABLISHED:
		return "ESTABLISHED"
	case FIN_WAIT_1:
		return "FIN_WAIT_1"
	case FIN_WAIT_2:
		return "FIN_WAIT_2"
	case CLOSE_WAIT:
		return "CLOSE_WAIT"
	case CLOSING:
		return "CLOSING"
	case LAST_ACK:
		return "LAST_ACK"
	case TIME_WAIT:
		return "TIME_WAIT"
	default:
		return "UNKNOWN"
	}

}
