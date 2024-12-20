package rip

// rip is constantly lisetning so we only need to call it once
// rip MUST be implemented with split horizon + poison reverse
import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"ipp/pkg/network"
	"ipp/pkg/protocol"
	"net"
	"net/netip"
	"time"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
)

var NetworkLayer *network.NetworkLayer

const (
	RequestCommand  = 1
	ResponseCommand = 2

	INFINITY = 16

	RIPEntrySize        = 12
	RIPPacketHeaderSize = 4
	MaxNumEntries       = 64

	EntryExpiredTime     = time.Second * 12
	UpdateInterval       = time.Second * 5
	CheckExpiredInterval = time.Second * 1
)

type RIPEntry struct {
	Cost    uint32
	Address [4]byte
	Mask    [4]byte
}

type RIPPacket struct {
	Command    uint16
	NumEntries uint16
	Entries    []RIPEntry
}

func (packet *RIPPacket) Marshal() ([]byte, error) {
	buff := new(bytes.Buffer)

	err := binary.Write(buff, binary.BigEndian, packet.Command)
	if err != nil {
		return nil, err
	}

	err = binary.Write(buff, binary.BigEndian, packet.NumEntries)
	if err != nil {
		return nil, err
	}

	for _, entry := range packet.Entries {
		err := binary.Write(buff, binary.BigEndian, entry.Cost)
		if err != nil {
			return nil, err
		}

		err = binary.Write(buff, binary.BigEndian, entry.Address)
		if err != nil {
			return nil, err
		}

		err = binary.Write(buff, binary.BigEndian, entry.Mask)
		if err != nil {
			return nil, err
		}
	}

	return buff.Bytes(), nil
}

func UnmarshalRIPPacket(data []byte) (*RIPPacket, error) {
	if len(data) < RIPPacketHeaderSize {
		return nil, errors.New("packet size less than header size was")
	}

	if (len(data)-RIPPacketHeaderSize)%RIPEntrySize != 0 {
		return nil, errors.New("packet size not a valid size")
	}
	ripPacket := RIPPacket{}

	ripPacket.Command = binary.BigEndian.Uint16(data[:2])
	if ripPacket.Command != RequestCommand && ripPacket.Command != ResponseCommand {
		return nil, errors.New("packet is not a request or response command")
	}

	numEntries := int(binary.BigEndian.Uint16(data[2:4]))

	if (ripPacket.Command == RequestCommand && numEntries != 0) || (numEntries > MaxNumEntries) {
		return nil, errors.New("invalid packet size")
	}

	packetNumEntries := (len(data) - RIPPacketHeaderSize) / RIPEntrySize

	if packetNumEntries != numEntries {
		return nil, errors.New("packet num entries does not match packet size")
	}

	for i := 0; i < numEntries; i++ {
		ripEntryOffset := RIPPacketHeaderSize + (i * RIPEntrySize)
		ripEntrySlice := data[ripEntryOffset : ripEntryOffset+RIPEntrySize]

		ripEntryCost := binary.BigEndian.Uint32(ripEntrySlice[:4])
		ripEntryAddress := [4]byte(ripEntrySlice[4:8])
		ripEntryMask := [4]byte(ripEntrySlice[8:])

		ripPacket.Entries = append(ripPacket.Entries, RIPEntry{
			Cost:    ripEntryCost,
			Address: ripEntryAddress,
			Mask:    ripEntryMask})
	}

	return &ripPacket, nil
}

func SendRIPResponseSafe(srcIP netip.Addr) error {
	var ripEntries []RIPEntry

	NetworkLayer.NetworkLayerLock.Lock()
	for fwdTableEntry, fwdTableValue := range NetworkLayer.FwdTable {

		cost := fwdTableValue.Cost

		if fwdTableValue.RouteType == network.RIPRoute {
			if *fwdTableValue.NextHop == srcIP {
				cost = INFINITY
			}
		} else if fwdTableEntry.AssignedPrefix.Contains(srcIP) {
			cost = INFINITY
		}

		ripEntry := RIPEntry{
			Cost:    cost,
			Address: fwdTableEntry.AssignedPrefix.Addr().As4(),
			Mask:    [4]byte(net.CIDRMask(fwdTableEntry.AssignedPrefix.Bits(), 32)),
		}

		ripEntries = append(ripEntries, ripEntry)

	}
	NetworkLayer.NetworkLayerLock.Unlock()

	packet := RIPPacket{
		Command:    ResponseCommand,
		NumEntries: uint16(len(ripEntries)),
		Entries:    ripEntries,
	}

	packetBytes, err := packet.Marshal()
	if err != nil {
		fmt.Println("Unable to marshal packet ", err)
		return err
	}

	err = NetworkLayer.SendPacketToDestIP(srcIP, protocol.RIPProtocolNum, packetBytes)

	if err != nil {
		return err
	}
	return nil
}

func RIPHandler(data []byte, params []interface{}) {

	packetHeader := params[0].(*ipv4header.IPv4Header)
	ripPacket, err := UnmarshalRIPPacket(data)
	if err != nil {
		fmt.Println("Error in unmarshalling rip packet: ", err)
		return
	}

	// seems like it would be actual address of src not virtual???
	srcIP := packetHeader.Src
	if ripPacket.Command == RequestCommand {
		err = SendRIPResponseSafe(srcIP)
		if err != nil {
			fmt.Printf("Error when responding to a request from %v -- %v", srcIP, err)
		}
	} else {
		updatedEntries := make([]RIPEntry, 0)
		for _, ripEntry := range ripPacket.Entries {
			destIP := netip.AddrFrom4(ripEntry.Address)
			newCost := ripEntry.Cost + 1
			if newCost >= INFINITY {
				continue
			}

			newNextHop := srcIP
			newFwdTableValue := network.CreateFwdTableValue(newCost, time.Now(), nil, &newNextHop, network.RIPRoute)
			updatedRIPEntry := RIPEntry{
				Cost:    newCost,
				Address: ripEntry.Address,
				Mask:    ripEntry.Mask,
			}

			// check if we already have entry in our fwd table
			currFwdEntryPointer, ok := NetworkLayer.GetEntryByPrefix(destIP)
			NetworkLayer.NetworkLayerLock.Lock()
			if !ok {
				prefixOnes, _ := net.IPMask(ripEntry.Mask[:]).Size()

				prefixString := fmt.Sprintf("%s/%d", destIP, prefixOnes)
				newPrefix, err := netip.ParsePrefix(prefixString)
				if err != nil {
					fmt.Println("Error parsing prefix ", err)
					continue
				}
				newFwdTableEntry := network.CreateFwdTableEntry(destIP, newPrefix.Masked())
				NetworkLayer.FwdTable[newFwdTableEntry] = newFwdTableValue
				updatedEntries = append(updatedEntries, updatedRIPEntry)
			} else if NetworkLayer.FwdTable[*currFwdEntryPointer].RouteType == network.RIPRoute {
				currFwdEntry := *currFwdEntryPointer
				oldCost := NetworkLayer.FwdTable[currFwdEntry].Cost
				oldNextHop := *NetworkLayer.FwdTable[currFwdEntry].NextHop

				if newCost < oldCost {
					NetworkLayer.FwdTable[currFwdEntry] = newFwdTableValue
					updatedEntries = append(updatedEntries, updatedRIPEntry)
				} else if newCost > oldCost && newNextHop == oldNextHop {
					NetworkLayer.FwdTable[currFwdEntry] = newFwdTableValue
					updatedEntries = append(updatedEntries, updatedRIPEntry)
				} else if newCost == oldCost && oldNextHop == newNextHop {
					NetworkLayer.FwdTable[currFwdEntry] = newFwdTableValue // refresh timeout
				}
			}
			NetworkLayer.NetworkLayerLock.Unlock()
		}

		if len(updatedEntries) != 0 {
			for _, ripNeighborAddr := range NetworkLayer.RIPNeighbors {
				SendRIPResponseSafe(ripNeighborAddr)
			}
		}

	}
}

func PeriodicExpiry(ipStack *protocol.IPStack) {
	ticker := time.NewTicker(CheckExpiredInterval)

	for {
		<-ticker.C
		now := time.Now()

		var expiredEntries []network.FwdTableEntry

		NetworkLayer.NetworkLayerLock.Lock()
		for entry, value := range NetworkLayer.FwdTable {
			if value.RouteType == network.RIPRoute {
				if (now.Sub(value.LastUpdatedTime) > EntryExpiredTime) || (value.Cost >= INFINITY) {
					expiredEntries = append(expiredEntries, entry)
				}
			}

		}

		for _, entry := range expiredEntries {
			delete(NetworkLayer.FwdTable, entry)
		}
		NetworkLayer.NetworkLayerLock.Unlock()
	}
}

func PeriodicUpdate(ipStack *protocol.IPStack) {
	ticker := time.NewTicker(UpdateInterval)

	for {
		for _, ripNeighborAddr := range NetworkLayer.RIPNeighbors {
			SendRIPResponseSafe(ripNeighborAddr)
		}
		<-ticker.C
	}
}

func InitialRIP(n *network.NetworkLayer) error {
	NetworkLayer = n
	ripPacket := RIPPacket{
		Command:    RequestCommand,
		NumEntries: 0,
	}

	packetBytes, err := ripPacket.Marshal()
	if err != nil {
		return err
	}
	for _, ripNeighborAddr := range NetworkLayer.RIPNeighbors {
		err = NetworkLayer.SendPacketToDestIP(ripNeighborAddr, protocol.RIPProtocolNum, packetBytes)
		if err != nil {
			return err
		}
	}

	return nil
}
