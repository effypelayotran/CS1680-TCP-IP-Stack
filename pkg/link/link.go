package link

import (
	"ipp/pkg/lnxconfig"
	"log"
	"net"
	"net/netip"
	"sync"
)

const (
	MaxMessageSize = 1400
)

type LinkLayer struct {
	Interfaces    []IPInterface
	PacketChannel *chan []byte
	LinkLayerLock sync.Mutex
}

type IPInterface struct {
	AssignedIP     netip.Addr
	AssignedPrefix netip.Prefix
	UDPPort        netip.AddrPort
	Neighbors      map[netip.Addr]netip.AddrPort
	InterfaceName  string
	State          InterfaceState
	Conn           *net.UDPConn
}

// Handle Interface State
type InterfaceState bool

const (
	INTERFACEUP   InterfaceState = true
	INTERFACEDOWN InterfaceState = false
)

func (l *LinkLayer) UpdateInterfaceState(intfToChange string, newState InterfaceState) (err error) {
	l.LinkLayerLock.Lock()
	defer l.LinkLayerLock.Unlock()
	for i := range l.Interfaces {
		if l.Interfaces[i].InterfaceName == intfToChange {
			// If newState is different from currentState
			if newState != l.Interfaces[i].State {
				l.Interfaces[i].State = InterfaceState(newState)
			}
		}
	}
	return nil
}

func (state InterfaceState) ToString() string {
	if state {
		return "up"
	} else {
		return "down"
	}
}

func InterfaceStateFromString(state_string string) InterfaceState {
	if state_string == INTERFACEDOWN.ToString() {
		return INTERFACEDOWN
	} else {
		return INTERFACEUP
	}
}

// Parse IPConfig into Interface Struct
func ParseLnxConfigToIpInterfaces(config *lnxconfig.IPConfig) []IPInterface {
	var interfacesList []IPInterface
	interfaceToNeighbors := make(map[string][]lnxconfig.NeighborConfig)
	for _, neighbor := range config.Neighbors {
		interfaceToNeighbors[neighbor.InterfaceName] = append(interfaceToNeighbors[neighbor.InterfaceName], neighbor)
	}

	for _, iface := range config.Interfaces {
		conn, err := InitInterfaceConn(iface.UDPAddr)
		if err != nil {
			log.Fatalln("Error creating UDP conn: ", err)
		}

		ipInterface := IPInterface{
			AssignedIP:     iface.AssignedIP,
			AssignedPrefix: iface.AssignedPrefix,
			UDPPort:        iface.UDPAddr,
			InterfaceName:  iface.Name,
			State:          INTERFACEUP, // State should be UP when first initialized
			Conn:           conn,
		}

		// Convert []lnxconfig.NeighborConfig to []*Neighbor
		ipInterface.Neighbors = make(map[netip.Addr]netip.AddrPort)
		for _, neighbor := range interfaceToNeighbors[iface.Name] {
			ipInterface.Neighbors[neighbor.DestAddr] = neighbor.UDPAddr
		}

		interfacesList = append(interfacesList, ipInterface)
	}

	return interfacesList
}

func InitInterfaceConn(UDPPort netip.AddrPort) (*net.UDPConn, error) {
	listenAddr, err := net.ResolveUDPAddr("udp4", UDPPort.String())
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp4", listenAddr)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func ReceivePacket(ipInterface *IPInterface, packetChannel chan []byte) {
	for {
		buffer := make([]byte, MaxMessageSize)
		_, err := ipInterface.Conn.Read(buffer)
		if err != nil {
			log.Fatalln("Error reading message:", err)
			return
		}

		packetChannel <- buffer
	}
}
