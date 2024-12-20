package cli

import (
	"fmt"
	"ipp/pkg/link"
	"ipp/pkg/protocol"
	"ipp/pkg/tcp"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

func HandleInput(command string, ipStack *protocol.IPStack, tcpRepl bool) {
	words := strings.Split(command, " ")

	if len(words) >= 3 && words[0] == "send" {
		HandleSend(words, ipStack)
	} else if len(words) == 1 && words[0] == "li" {
		ListInterfaces(ipStack)
	} else if len(words) == 1 && words[0] == "ln" {
		ListNeighbors(ipStack)
	} else if len(words) == 1 && words[0] == "lr" {
		ListRoutes(ipStack)
	} else if words[0] == "down" {
		if len(words) != 2 {
			fmt.Println("Invalid usage. Use 'down <ifname>'")
			return
		} else {
			HandleDownCommand(words, ipStack)
		}
	} else if words[0] == "up" {
		if len(words) != 2 {
			fmt.Println("Invalid usage. Use 'up <ifname>'")
			return
		} else {
			HandleUpCommand(words, ipStack)
		}
	} else if tcpRepl {
		if len(words) == 3 && words[0] == "c" {
			HandleConnect(words)
		} else if len(words) == 2 && words[0] == "a" {
			HandleAccept(words)
		} else if len(words) == 1 && words[0] == "ls" {
			ListSockets()
		} else if len(words) >= 3 && words[0] == "s" {
			HandleSendSocket(words)
		} else if len(words) == 3 && words[0] == "r" {
			HandleReceiveSocket(words)
		} else if len(words) == 2 && words[0] == "cl" {
			HandleClose(words)
		} else if len(words) == 4 && words[0] == "sf" {
			HandleSendFile(words)
		} else if len(words) == 3 && words[0] == "rf" {
			HandleReceiveFile(words)
		}
	} else {
		fmt.Println("Invalid commaind")
	}
}

func HandleSendSocket(words []string) {
	// s <socket ID> <data>
	socketId, err := strconv.Atoi(words[1])
	if err != nil {
		fmt.Printf("Invalid socket Id: %s", words[1])
		return
	}

	socket := tcp.GetSocketById(socketId)
	if socket == nil {
		fmt.Printf("socketId %d doesn't exist\n", socketId)
		return
	}

	allWords := strings.Join(words[2:], " ")
	bytesWritten, err := socket.Conn.VWrite([]byte(allWords))
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Printf("Sent %d bytes\n", bytesWritten)
	}
}

func HandleReceiveSocket(words []string) {
	// r <socket ID> <numbytes>
	socketId, err := strconv.Atoi(words[1])
	if err != nil {
		fmt.Printf("Invalid socket Id: %s", words[1])
	}

	bytesToRead, err := strconv.Atoi(words[2])
	if err != nil {
		fmt.Printf("Invalid bytesToRead: %s\n", words[2])
		return
	}

	payload := make([]byte, bytesToRead)
	totalBytesRead := 0
	socket := tcp.GetSocketById(socketId)
	if socket == nil {
		fmt.Printf("socketId %d doesn't exist\n", socketId)
		return
	}

	totalBytesRead, err = socket.Conn.VRead(payload)
	if err != nil {
		fmt.Println("Error while reading:", err)
	}

	fmt.Printf("Read %d bytes: ", totalBytesRead)
	fmt.Println(string(payload[:totalBytesRead]))
}

func HandleSend(words []string, ipStack *protocol.IPStack) {
	allWords := strings.Join(words[2:], " ")

	destIP, err := netip.ParseAddr(words[1])
	if err != nil {
		fmt.Println("Could not parse addr ", err)
		return
	}

	err = ipStack.SendIP(destIP, protocol.TestProtocolNum, []byte(allWords))
	if err != nil {
		fmt.Println("Error sending packet ", err)
	}
}

func ListInterfaces(ipStack *protocol.IPStack) {
	fmt.Printf("%-6s %-15s %s\n", "Name", "Addr/Prefix", "State")
	fmt.Println(strings.Repeat("-", 33))

	//TODO: Add Lock to LinkLayer
	//TODO: Add Function from other repo that allows you to change Interface state
	for _, intf := range ipStack.LinkLayer.Interfaces {
		state := intf.State.ToString()
		fmt.Printf("%-6s %s/%d     %s\n", intf.InterfaceName, intf.AssignedIP, intf.AssignedPrefix.Bits(), state)
	}
}

func ListNeighbors(ipStack *protocol.IPStack) {
	fmt.Printf("%-6s%-15s%-15s\n", "Iface", "VIP", "UDPAddr")

	for _, intf := range ipStack.LinkLayer.Interfaces {
		if intf.State == link.INTERFACEUP {
			for neighborIP, udpAddr := range intf.Neighbors {
				fmt.Printf("%-6s%-15s%-15s\n", intf.InterfaceName, neighborIP.String(), udpAddr.String())
			}
		}
	}
}

// Define a function for the lr command
func ListRoutes(ipStack *protocol.IPStack) {
	fmt.Println("T       Prefix         Next hop       Cost")

	for entry, value := range ipStack.NetworkLayer.FwdTable {
		var routeType string
		var nextHop string
		var cost string

		switch value.RouteType {
		case 1:
			routeType = "L"
			if value.Interface != nil {
				nextHop = fmt.Sprintf("LOCAL:%s", value.Interface.InterfaceName)
			}
			cost = "0"
		case 2:
			routeType = "S"
			if value.NextHop != nil {
				nextHop = value.NextHop.String()
			}
			cost = "-"
		case 3:
			routeType = "R"
			if value.NextHop != nil {
				nextHop = value.NextHop.String()
			}
			cost = fmt.Sprintf("%d", value.Cost)
		}

		fmt.Printf("%s  %-15s  %-14s  %s\n", routeType, entry.AssignedPrefix, nextHop, cost)
	}
}

func HandleDownCommand(words []string, ipStack *protocol.IPStack) {
	ifname := words[1]

	ipStack.NetworkLayer.UpdateInterfaceState(ifname, link.INTERFACEDOWN)
	ipStack.LinkLayer.UpdateInterfaceState(ifname, link.INTERFACEDOWN)

}

func HandleUpCommand(words []string, ipStack *protocol.IPStack) {
	ifname := words[1]

	ipStack.NetworkLayer.UpdateInterfaceState(ifname, link.INTERFACEUP)
	ipStack.LinkLayer.UpdateInterfaceState(ifname, link.INTERFACEUP)
}

func HandleConnect(words []string) {
	destIP, err := netip.ParseAddr(words[1])
	if err != nil {
		return
	}

	port, err := strconv.Atoi(words[2])
	if err != nil {
		fmt.Println("Invalid TCP port")
		return
	}

	go func() {
		_, err = tcp.VConnect(destIP, uint16(port))
		if err != nil {
			fmt.Println(err)
		}
	}()
}

func HandleAccept(words []string) {
	port, err := strconv.Atoi(words[1])
	if err != nil {
		fmt.Printf("Invalid TCP port")
	}

	listener, err := tcp.VListen(uint16(port))
	if err != nil {
		fmt.Println(err)
		return
	}

	go func() {
		for {
			listener.VAccept()
		}
	}()
}

func ListSockets() {
	tcp.PrintSockets()
}

func HandleClose(words []string) {
	// check if its a listener
	socketId, err := strconv.Atoi((words[1]))
	if err != nil {
		fmt.Println(err)
		return
	}

	// call vloce on listenenr socket
	listener := tcp.GetListenerBySocketId(socketId)

	if listener != nil {
		err := listener.VClose()
		fmt.Println(err)
	} else {
		// if not call normal close
		socket := tcp.GetSocketById(socketId)
		if socket == nil {
			fmt.Println("error: socket does not exist")
			return
		}

		err := socket.Conn.VClose()
		if err != nil {
			fmt.Println(err)
		}
	}
}

func HandleSendFile(words []string) {
	// sf <filename> <ip> <port>
	payload, err := os.ReadFile(words[1])
	if err != nil {
		fmt.Println(err)
		return
	}

	destIP, err := netip.ParseAddr(words[2])
	if err != nil {
		fmt.Println(err)
		return
	}

	port, err := strconv.Atoi(words[3])
	if err != nil {
		fmt.Println(err)
		return
	}

	tcpConn, err := tcp.VConnect(destIP, uint16(port))
	if err != nil {
		fmt.Println(err)
	}

	go func() {
		//fmt.Printf("sf: going to write: %s\n", payload)
		bytesWritten, err := tcpConn.VWrite(payload)
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Printf("Wrote %d bytes from file %s\n", bytesWritten, words[1])
		}
		tcpConn.VClose()
	}()
}

// This command starts a new thread that waits for a connection on the specified listening port.
// After a client connects, it receives data until the sender closes the connection, writing the output to the destination file.
func HandleReceiveFile(words []string) {
	// rf <dest file> <port>
	filename := words[1]
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0777)
	if err != nil {
		fmt.Println(err)
		return
	}

	port, err := strconv.Atoi(words[2])
	if err != nil {
		fmt.Println(err)
		return
	}

	tcpListener, err := tcp.VListen(uint16(port))
	if err != nil {
		fmt.Println(err)
		return
	}

	go func() {
		tcpConn, err := tcpListener.VAccept()
		if err != nil {
			fmt.Println(err)
			return
		}

		tcpSocket := tcp.GetSocketByConn(tcpConn)

		totalBytesRead := 0
		totalBytesWritten := 0
		buffer := make([]byte, tcp.MaxFileSize)

		// fmt.Println("receive file: about to start")
		for tcpSocket.ConnState == tcp.ESTABLISHED {
			bytesRead, err := tcpConn.VRead(buffer[totalBytesRead:])
			// if (totalBytesWritten >= 990000) {
			// 	totalBytesRead += bytesRead
			// 	break
			// }
			if err != nil {
				fmt.Println("rf: ", err)
				break
			} else {
				// fmt.Printf("rf bytes read %d\n", bytesRead)
				// _, err := file.Write(buffer[:bytesRead])
				// if err != nil {
				// 	fmt.Println(err)
				// 	return
				// }

				totalBytesRead += bytesRead
				// fmt.Printf("Received %d total bytes\n", totalBytesRead)

				

			}
		}
		fmt.Println("sender closed connection")
		// if !tcpSocket.ReadBuffer.IsEmpty() {
		// 	bytesRead, err := tcpSocket.ReadBuffer.Read(buffer[totalBytesRead:])
		// 	if err != nil {
		// 		fmt.Println("rf: ", err)
		// 	} else {
		// 		// fmt.Printf("rf bytes read %d\n", bytesRead)
		// 		// _, err := file.Write(buffer[:bytesRead])
		// 		// if err != nil {
		// 		// 	fmt.Println(err)
		// 		// 	return
		// 		// }

		// 		totalBytesRead += bytesRead
		// 		fmt.Printf("Received %d total bytes\n", totalBytesRead)
		// 		// n, err := file.Write(buffer[:totalBytesRead])
		// 		// if err != nil {
		// 		// 	fmt.Println(err)
		// 		// 	return
		// 		// }
		// 		// totalBytesWritten += n
		// 	}
		// }

		totalBytesWritten ,err = file.Write(buffer[:totalBytesRead])
		if err != nil {
					fmt.Println(err)
					return
				}
		fmt.Printf("Total bytes written to file: %d\n", totalBytesWritten)
		// fmt.Printf("Received %d total bytes\n", totalBytesRead)
		file.Close()
		err = tcpConn.VClose()
		if err != nil {
			fmt.Println(err)
		}
		err = tcpListener.VClose()
		if err != nil {
			fmt.Println(err)
		}
	}()

}
