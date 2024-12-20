package protocol

import (
	"ipp/pkg/link"
	"ipp/pkg/lnxconfig"
	"ipp/pkg/network"
	"fmt"
	"net/netip"

	"github.com/google/netstack/tcpip/header"
	ipv4header "github.com/brown-csci1680/iptcp-headers"
)

type IPStack struct {
	Name         string
	NetworkLayer *network.NetworkLayer
	LinkLayer    *link.LinkLayer
}

const (
	TestProtocolNum = 0
	RIPProtocolNum = 200
	TCPProtocolNum = uint8(header.TCPProtocolNumber)
)

func Initialize(configInfo *lnxconfig.IPConfig) (*IPStack, error) {
	ipInterfaces := link.ParseLnxConfigToIpInterfaces(configInfo)

	packetChannel := make(chan []byte, 1)
	linkLayer := &link.LinkLayer{Interfaces: ipInterfaces, PacketChannel: &packetChannel}
	networkLayer := network.InitNetworkLayer(ipInterfaces, configInfo.StaticRoutes, packetChannel)

	return &IPStack{Name: "", NetworkLayer: networkLayer, LinkLayer: linkLayer}, nil
}

func (ipStack *IPStack) SendIP(dst netip.Addr, protocolNum uint8, data []byte) error {
	err := ipStack.NetworkLayer.SendPacketToDestIP(dst, protocolNum, data)
	return err
}

func (ipStack *IPStack) RegisterRecvHandler(protocolNum uint8, callbackFunc network.HandlerFunc) {
	ipStack.NetworkLayer.RegisterHandlers(protocolNum, callbackFunc)
}

func TestProtocolHandler(data []byte, params []interface{}) {
	hdr := params[0].(*ipv4header.IPv4Header)
	fmt.Print("Received test packet:  ")
	fmt.Printf("Src: %s, ", hdr.Src.String())
	fmt.Printf("Dst: %s, ", hdr.Dst.String())
	fmt.Printf("TTL: %d, ", hdr.TTL)
	fmt.Printf("Data: %s\n", string(data))
}

