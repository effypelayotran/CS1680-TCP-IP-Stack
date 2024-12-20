package network

import (
	"errors"
	"fmt"
	"ipp/pkg/link"
	"net"
	"net/netip"
	"sync"
	"time"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
	"github.com/google/netstack/tcpip/header"
)

// TODO: remove StaticRuotes, Update parser and packet handling to match
type HandlerFunc = func([]byte, []interface{})
type RouteType int

const (
	LocalRoute  = 1
	StaticRoute = 2
	RIPRoute    = 3
)

type FwdTableValue struct {
	Cost            uint32
	LastUpdatedTime time.Time
	Interface       *link.IPInterface // represent nextHOP
	NextHop         *netip.Addr       // either has a NextHop or an Interface
	RouteType       RouteType
}

type FwdTableEntry struct {
	AssignedIP     netip.Addr // represents the final Dest-- if local this is the same assigned IP as the next HOP
	AssignedPrefix netip.Prefix
}

type NetworkLayer struct {
	FwdTable map[FwdTableEntry]FwdTableValue
	// StaticRoutes  map[netip.Prefix]netip.Addr // Contains Next HOP?
	Handlers      map[uint8]HandlerFunc
	PacketChannel *chan []byte
	RIPNeighbors  []netip.Addr

	NetworkLayerLock sync.Mutex
}

func CreateFwdTableValue(givenCost uint32, givenTime time.Time, givenIntf *link.IPInterface, givenNextHop *netip.Addr, givenRouteType RouteType) FwdTableValue {
	return FwdTableValue{
		Cost:            givenCost, // for RIP
		LastUpdatedTime: givenTime,
		Interface:       givenIntf,
		NextHop:         givenNextHop,
		RouteType:       givenRouteType,
	}
}

func CreateFwdTableEntry(assIp netip.Addr, assPrefix netip.Prefix) FwdTableEntry {
	return FwdTableEntry{
		AssignedIP:     assIp,
		AssignedPrefix: assPrefix,
	}
}

func InitNetworkLayer(interfaces []link.IPInterface, staticRoutes map[netip.Prefix]netip.Addr, packetChannel chan []byte) (n *NetworkLayer) {
	fwdTable := make(map[FwdTableEntry]FwdTableValue)
	handlers := make(map[uint8]HandlerFunc)

	for _, ipInterface := range interfaces {
		currentInterface := ipInterface
		// set RoutingType to local here
		entry := CreateFwdTableEntry(currentInterface.AssignedIP, currentInterface.AssignedPrefix)
		fwdTable[entry] = CreateFwdTableValue(0, time.Now(), &currentInterface, nil, LocalRoute)

	}

	for prefix, addr := range staticRoutes {
		entry := CreateFwdTableEntry(addr, prefix)
		fwdTable[entry] = CreateFwdTableValue(0, time.Now(), nil, &addr, StaticRoute)
	}

	return &NetworkLayer{FwdTable: fwdTable, Handlers: handlers, PacketChannel: &packetChannel}
}

func (n *NetworkLayer) RegisterHandlers(protocolNum uint8, hf HandlerFunc) {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()

	n.Handlers[protocolNum] = hf
}

func (n *NetworkLayer) checkIfLocalIP(destIP netip.Addr) (*link.IPInterface, bool) {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()
	// for each (key , value) in the data structure (FwdTable)
	for FwdTableEntry, FwdTableValue := range n.FwdTable {
		if FwdTableValue.RouteType == LocalRoute {
			if FwdTableEntry.AssignedIP == destIP {
				return FwdTableValue.Interface, true
			}
		}
	}
	return nil, false
}

func (n *NetworkLayer) getInterfaceByPrefix(destIP netip.Addr) (*link.IPInterface, bool) {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()

	// for each (key , value) in the data structure (FwdTable)
	for entry, value := range n.FwdTable {
		if value.RouteType == LocalRoute && value.Interface.State == link.INTERFACEUP {
			if entry.AssignedPrefix.Contains(destIP) {
				return value.Interface, true
			}
		}
	}
	return nil, false
}

// Given a dest addr, retrieve the next hop by matching on fwd table prefix entries
func (n *NetworkLayer) getNextHopbyDestIP(destIP netip.Addr) *netip.Addr {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()

	for fwdTableEntry, fwdTableValue := range n.FwdTable {
		if fwdTableEntry.AssignedPrefix.Contains(destIP) {
			return fwdTableValue.NextHop
		}
	}
	return nil
}

func (n *NetworkLayer) getRemoteAddrByNeighborIP(destIP netip.Addr, ipInterface link.IPInterface) (*netip.AddrPort, bool) {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()

	for destAddr, udpAddr := range ipInterface.Neighbors {
		if destAddr == destIP {
			return &udpAddr, true
		}
	}
	return nil, false
}

func (n *NetworkLayer) GetEntryByAddress(addr netip.Addr) (*FwdTableEntry, bool) {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()

	for fwdTableEntry := range n.FwdTable {
		if fwdTableEntry.AssignedIP == addr {
			return &fwdTableEntry, true
		}
	}
	return nil, false
}

func (n *NetworkLayer) GetEntryByPrefix(addr netip.Addr) (*FwdTableEntry, bool) {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()

	for fwdTableEntry := range n.FwdTable {
		if fwdTableEntry.AssignedPrefix.Contains(addr) {
			return &fwdTableEntry, true
		}

	}
	return nil, false
}

func ComputeChecksum(b []byte) uint16 {
	checksum := header.Checksum(b, 0)

	// Invert the checksum value.  Why is this necessary?
	// This function returns the inverse of the checksum
	// on an initial computation.  While this may seem weird,
	// it makes it easier to use this same function
	// to validate the checksum on the receiving side.
	// See ValidateChecksum in the receiver file for details.
	checksumInv := checksum ^ 0xffff

	return checksumInv
}

func ValidateChecksum(b []byte, fromHeader uint16) uint16 {
	checksum := header.Checksum(b, fromHeader)
	return checksum
}

const (
	MaxHops = 16
)

func (n *NetworkLayer) HandlePacket(buffer []byte) error {
	packetHeader, err := ipv4header.ParseHeader(buffer) // ParseHeader() func built-in to ipv4 package
	if err != nil {
		fmt.Println("Unable to Parse Header in HandlePacket")
		return err
	}

	headerSize := packetHeader.Len
	headerBytes := buffer[:headerSize]
	checksumFromHeader := uint16(packetHeader.Checksum)
	computedChecksum := ValidateChecksum(headerBytes, checksumFromHeader)

	if computedChecksum != checksumFromHeader {
		return errors.New("invalid checksum")
	}

	destIP := packetHeader.Dst
	msgBytes := buffer[headerSize:packetHeader.TotalLen]

	// find out if dest ip is our ip address
	myInterface, ok := n.checkIfLocalIP(destIP)

	// we are the dest, send to api
	if ok {
		if myInterface.State == link.INTERFACEDOWN {
			return nil
		}

		// we are the destination, call the handler for the appropriate application
		handler, ok := n.Handlers[uint8(packetHeader.Protocol)]
		if !ok {
			return errors.New("invalid protocol number in IP header")
		}
		
		handler(msgBytes, []interface{}{packetHeader})
		return nil
	}

	// check if dest ip matches interface prefix
	var localInterface *link.IPInterface
	localInterface, ok = n.getInterfaceByPrefix(destIP)

	// check for next hop route
	if !ok {
		nextHopIP := n.getNextHopbyDestIP(destIP)
		if nextHopIP == nil {
			// error, don't have default route
			return errors.New("no next hop found")
		}
		destIP = *nextHopIP
		localInterface, ok = n.getInterfaceByPrefix(*nextHopIP)
		if !ok {
			return errors.New("could not find interface based on next hop")
		}
	}

	// check if destIP is in neighbors
	remoteAddr, ok := n.getRemoteAddrByNeighborIP(destIP, *localInterface)
	if !ok {
		return errors.New("should never happen")
	}

	remoteUDPAddr, err := net.ResolveUDPAddr("udp4", remoteAddr.String())
	if err != nil {
		return err
	}

	packetHeader.TTL -= 1
	if packetHeader.TTL <= 0 {
		return nil
	}

	packetHeader.Checksum = 0
	headerBytes, err = packetHeader.Marshal()
	if err != nil {
		fmt.Println("Error marshalling header:  ", err)
		return err
	}

	packetHeader.Checksum = int(ComputeChecksum(headerBytes))
	headerBytes, err = packetHeader.Marshal()
	if err != nil {
		fmt.Println("Error marshalling header:  ", err)
		return err
	}

	fullPacket := append(headerBytes, msgBytes...)
	localInterface.Conn.WriteToUDP(fullPacket, remoteUDPAddr)

	return nil
}

// call this func in CLI
func (n *NetworkLayer) SendPacketToDestIP(destIP netip.Addr, procotol uint8, msg []byte) (err error) {
	var nextHopInterface *link.IPInterface = nil
	var ok bool = false
	var isLocalHost bool = false
	var nextIP netip.Addr = destIP

	//check if the destIP is matches extacly to the IP of one of our interfaces
	nextHopInterface, ok = n.checkIfLocalIP(destIP)
	isLocalHost = ok

	if !ok {
		nextHopInterface, ok = n.getInterfaceByPrefix(destIP) // gives you interface that has neighbor match; not neighbor itself
	}

	if !ok {
		nextHopIP := n.getNextHopbyDestIP(destIP)
		if nextHopIP == nil {
			return errors.New("no next hop found for dest ip")
		}

		nextIP = *nextHopIP
		nextHopInterface, ok = n.getInterfaceByPrefix(*nextHopIP)

		if !ok {
			return errors.New("Can't reach the destIP " + destIP.String())
		}
	}
	packetHeader := ipv4header.IPv4Header{
		Version:  4,
		Len:      20,
		TOS:      0,
		TotalLen: ipv4header.HeaderLen + len(msg),
		ID:       0,
		Flags:    0,
		FragOff:  0,
		TTL:      MaxHops, // Set to 16
		Protocol: int(procotol),
		Checksum: 0, // Initialized to 0 until computeChecksum() is called
		Src:      nextHopInterface.AssignedIP,
		Dst:      destIP,
		Options:  []byte{},
	}

	headerBytes, err := packetHeader.Marshal()
	if err != nil {
		return err
	}

	packetHeader.Checksum = int(ComputeChecksum(headerBytes))

	headerBytes, err = packetHeader.Marshal()
	if err != nil {
		return err
	}

	bytesToSend := make([]byte, 0, len(headerBytes)+len(msg))
	bytesToSend = append(bytesToSend, headerBytes...)
	bytesToSend = append(bytesToSend, []byte(msg)...)
	if isLocalHost {
		*n.PacketChannel <- bytesToSend
	} else {
		remoteAddr, ok := n.getRemoteAddrByNeighborIP(nextIP, *nextHopInterface)

		if !ok {
			return errors.New("should never happen, didn't find neighbor")
		}

		remoteAddrString, err := net.ResolveUDPAddr("udp4", remoteAddr.String())
		if err != nil {
			return err
		}

		nextHopInterface.Conn.WriteToUDP(bytesToSend, remoteAddrString)
	}
	return nil
}

func (n *NetworkLayer) UpdateInterfaceState(intfToChange string, newState link.InterfaceState) (err error) {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()

	for _, value := range n.FwdTable {
		if value.RouteType == 1 {
			if value.Interface.InterfaceName == intfToChange {
				// If newState is differnet from currentState
				if newState != value.Interface.State {
					value.Interface.State = newState
				}
			}
		}

	}
	return nil
}