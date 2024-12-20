package main

import (
	"bufio"
	"fmt"
	"ipp/pkg/cli"
	"ipp/pkg/link"
	"ipp/pkg/lnxconfig"
	"ipp/pkg/protocol"
	"ipp/pkg/rip"
	"os"
	"sync"
)

func main() {
	//call initialize API
	// set RIP Neighbors in network layer
	// register RIP handler

	if len(os.Args) != 3 {
		fmt.Printf("Usage:  %s --config <configFile>\n", os.Args[0])
		os.Exit(1)
	} else if os.Args[1] != "--config"{
		fmt.Printf("Usage:  %s --config <configFile>\n", os.Args[0])
		os.Exit(1)
	}
	
	fileName := os.Args[2]

	lnxConfig, err := lnxconfig.ParseConfig(fileName)
	if err != nil {
		panic(err)
	}

	ipStack, err := protocol.Initialize(lnxConfig)
	if err != nil {
		panic(err)
	}
	ipStack.NetworkLayer.RIPNeighbors = lnxConfig.RipNeighbors

	ipStack.RegisterRecvHandler(protocol.TestProtocolNum, protocol.TestProtocolHandler)
	ipStack.RegisterRecvHandler(protocol.RIPProtocolNum, rip.RIPHandler)

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
			ipStack.NetworkLayer.HandlePacket(packet)
		}
	}()

	go func() {
		for {
			fmt.Printf("> ")
			command := <-keyboardChan
			cli.HandleInput(command, ipStack, false)
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

	rip.InitialRIP(ipStack.NetworkLayer)
	go rip.PeriodicExpiry(ipStack)
	go rip.PeriodicUpdate(ipStack)
	
	wg.Wait()
}
