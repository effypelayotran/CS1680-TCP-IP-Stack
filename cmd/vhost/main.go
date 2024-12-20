package main

import (
	"bufio"
	"fmt"
	"ipp/pkg/cli"
	"ipp/pkg/link"
	"ipp/pkg/lnxconfig"
	"ipp/pkg/protocol"
	"ipp/pkg/tcp"
	"os"
	"sync"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Printf("Usage:  %s --config <configFile>\n", os.Args[0])
		os.Exit(1)
	} else if os.Args[1] != "--config" {
		fmt.Printf("Usage:  %s --config <configFile>\n", os.Args[0])
		os.Exit(1)
	}
	fileName := os.Args[2]
	
	// Parse the file
	lnxConfig, err := lnxconfig.ParseConfig(fileName)
	if err != nil {
		panic(err)
	}

	ipStack, err := protocol.Initialize(lnxConfig)
	if err != nil {
		panic(err)
	}

	ipStack.RegisterRecvHandler(protocol.TestProtocolNum, protocol.TestProtocolHandler)
	ipStack.RegisterRecvHandler(protocol.TCPProtocolNum, tcp.TcpHandler)

	// right now manually close interfaces?
	for _, ipInterface := range ipStack.LinkLayer.Interfaces {
		defer ipInterface.Conn.Close()
	}

	keyboardChan := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			keyboardChan <- line
		}
	}()

	go func() {
		for {
			packet := <-*ipStack.LinkLayer.PacketChannel
			err := ipStack.NetworkLayer.HandlePacket(packet)
			if err != nil {
				fmt.Println("Error handling packet: ", err)
			}
		}
	}()

	go func() {
		for {
			fmt.Printf("> ")
			command := <-keyboardChan
			cli.HandleInput(command, ipStack, true)
		}
	}()

	//listen on interfaces (udp port)
	var wg sync.WaitGroup
	for _, ipInterface := range ipStack.LinkLayer.Interfaces {
		wg.Add(1)
		go func(ipInterface link.IPInterface) {
			defer wg.Done()
			link.ReceivePacket(&ipInterface, *ipStack.LinkLayer.PacketChannel)
		}(ipInterface)
	}
	
	tcp.Init(ipStack)
	wg.Wait()
}
