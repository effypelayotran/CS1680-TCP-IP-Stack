package printer

// contains print functions for debugging

import (
	"fmt"
	"ipp/pkg/link"
	"ipp/pkg/lnxconfig"
	"ipp/pkg/network"
	"net/netip"
)

func PrintInterfaces(interfaces []link.IPInterface) {
	// Demo: print out the information about the interfaces in list
	if len(interfaces) == 0 {
		fmt.Println("ipInterfaces is empty")
	} else {
		for _, iface := range interfaces {
			fmt.Printf("Interface Name: %s\n", iface.InterfaceName)
			fmt.Printf("Assigned IP: %s\n", iface.AssignedIP)
			fmt.Printf("Assigned Prefix: %s\n", iface.AssignedPrefix)
			fmt.Printf("UDPPort: %s\n", iface.UDPPort)
			fmt.Printf("Conn: %s\n", iface.Conn.LocalAddr())
			for destAddr, udpAddr := range iface.Neighbors {
				fmt.Printf("Neighbor DestAddr: %s\n", destAddr)
				fmt.Printf("Neighbor UDPAddr: %s\n", udpAddr)
			}
		}
	}
}

func PrintIPAddr(lnxConfig lnxconfig.IPConfig) {
	// Demo:  print out the IP for each interface in this config
	for _, iface := range lnxConfig.Interfaces {
		prefixForm := netip.PrefixFrom(iface.AssignedIP, iface.AssignedPrefix.Bits())
		fmt.Printf("%s has IP %s\n", iface.Name, prefixForm.String())
	}
}

func PrintInitializedFwdTable(n *network.NetworkLayer) {
	n.NetworkLayerLock.Lock()
	defer n.NetworkLayerLock.Unlock()

	for fwdTableEntry := range n.FwdTable {
		fwdTableValue := n.FwdTable[fwdTableEntry]
		if fwdTableValue.RouteType == network.LocalRoute {
			fmt.Printf("Entry: %s/%s\n", fwdTableEntry.AssignedIP, fwdTableEntry.AssignedPrefix)
			fmt.Printf("Port: %s\n", fwdTableValue.Interface.UDPPort.String())
			fmt.Printf("Cost: %d\n", fwdTableValue.Cost)
			fmt.Printf("Last Updated Time: %s\n", fwdTableValue.LastUpdatedTime)
			fmt.Printf("Interface Name: %s\n", fwdTableValue.Interface.InterfaceName)
			fmt.Println("--------------------------------------")
		}
		//fmt.Println(fwdTableEntry.AssignedIP.String())

	}

	for fwdTableEntry, fwdTableValue := range n.FwdTable {
		if fwdTableValue.RouteType == network.StaticRoute {
			fmt.Printf("Default Route: %s via %s\n", fwdTableEntry.AssignedPrefix, fwdTableValue.NextHop)
		}
	}

	fmt.Println("--------------------------------------")
}
