package tcp

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/netstack/tcpip/header"
	"github.com/smallnest/ringbuffer"
)

// Listen Socket API
func VListen(port uint16) (*VTCPListener, error) {
	tcp.Lock.Lock()
	defer tcp.Lock.Unlock()

	// check to make sure given port number is not already used in tcp layer's listener table
	_, found := tcp.ListenersTable[port]
	if found {
		return nil, errors.New("vlisten: port already in use")
	}

	// create a vtcplistener struct for this port num, add it to our listener table
	tcp.ListenersTable[port] = &VTCPListener{
		SocketID:      getNextSocketID(),
		IPAddress:     tcp.LocalIP,
		PortNumber:    port,
		PacketChannel: make(chan *TCPPacket),
		StopFlag:      make(chan bool),
	}

	// return the listener struct we just made
	return tcp.ListenersTable[port], nil
}

func (listener *VTCPListener) VAccept() (*VTCPConn, error) {
	// receive a syn packet
	select {
	case packet := <-listener.PacketChannel:
		if packet.Header.Flags != header.TCPFlagSyn {
			return nil, errors.New("flag is not SYN in VAccept")
		}

		fmt.Println("2. received syn, ", packet.Header.SeqNum, ", ", packet.Header.AckNum)
		conn := VTCPConn{
			LocalIP:    listener.IPAddress,
			LocalPort:  listener.PortNumber,
			RemoteIP:   packet.SrcIP,
			RemotePort: packet.Header.SrcPort,
		}

		// after receiving the initial syn, make the first socket, set its state to SYN_RECEIVED, and then send a syn-ack packet.
		socket, err := MakeTCPSocket(SYN_RECEIVED, &conn, packet.Header.SeqNum)
		if err != nil {
			return nil, err
		}
		// fmt.Println("vaccept socket: initiail seq num", socket.InitSeqNum)
		// fmt.Println("vaccept socket: initiail ack num", socket.RemoteISN)

		tcp.Lock.Lock()
		tcp.SocketTable[conn] = socket
		tcp.Lock.Unlock()

		// TODO: cleanup so that we have a functon that consturcts packet, marshals, and sends it through ip
		// send syn/ ack packet
		tcpHdr := header.TCPFields{
			SrcPort:       listener.PortNumber,
			DstPort:       packet.Header.SrcPort,
			SeqNum:        socket.InitSeqNum,
			AckNum:        packet.Header.SeqNum + 1,
			DataOffset:    header.TCPMinimumSize,
			Flags:         header.TCPFlagSyn | header.TCPFlagAck,
			WindowSize:    MaxBufferSize,
			Checksum:      0,
			UrgentPointer: 0,
		}

		socket.NextExpectedByte = 1
		synAckPacket := TCPPacket{
			SrcIP:  tcp.LocalIP,
			Header: tcpHdr,
			Data:   make([]byte, 0),
		}

		packetBytes := synAckPacket.Marshal()

		// send the syn-ack packet
		err = tcp.IPStack.SendIP(packet.SrcIP, uint8(header.TCPProtocolNumber), packetBytes)
		if err != nil {
			return nil, err
		}
		socket.ConnState = SYN_SENT
		fmt.Println("3. sent syn-ack, ", synAckPacket.Header.SeqNum, ", ", synAckPacket.Header.AckNum)

		// expects an ack flag in return. once we have ack set the conn as ESTABLISHED
		select {
		case packet = <-socket.PacketChannel:
			if packet.Header.Flags == header.TCPFlagAck {
				// check that sequence num is correct
				if packet.Header.SeqNum != socket.RemoteISN+1 {
					return nil, errors.New("wrong seq num received")
				} else if packet.Header.AckNum != socket.InitSeqNum+1 {
					return nil, errors.New("wrongs ack num received")
				} else {
					fmt.Println("6. received ack, ", packet.Header.SeqNum, ", ", packet.Header.AckNum)
					socket.ConnState = ESTABLISHED
					socket.RemoteWindowSize = packet.Header.WindowSize

					go socket.HandleConn()
					return &conn, nil
				}
			} else {
				return nil, errors.New("wrong flag received")
			}
		case <-listener.StopFlag:
			fmt.Println("socket has been closed")
			return nil, nil
		}

	case <-listener.StopFlag:
		fmt.Println("socket has been closed")
		return nil, nil
	}
}

func (listener *VTCPListener) VClose() error {
	fmt.Println("calling close on a listener")
	// 	Closes this listening socket, removing it from the socket table. No new connection may be made on this socket.
	// Any pending requests to create new connections should be deleted.
	// This method should return an error if the close process fails (eg, if the socket is already closed.)

	tcp.Lock.Lock()
	_, found := tcp.ListenersTable[listener.PortNumber]
	if !found {
		tcp.Lock.Unlock()
		return errors.New("socket is already closed")
	}
	
	delete(tcp.ListenersTable, listener.PortNumber)
	tcp.Lock.Unlock()
	listener.StopFlag <- true
	return nil
}

// Normal socket API
func VConnect(remoteIPAddr netip.Addr, remotePort uint16) (*VTCPConn, error) {
	// TODO: add lock stuff
	tcp.Lock.Lock()

	// create conn struct
	conn := VTCPConn{
		LocalIP:    tcp.LocalIP,
		LocalPort:  getUnusedPort(),
		RemoteIP:   remoteIPAddr,
		RemotePort: remotePort,
	}

	// check if the connection has been previously established
	_, ok := tcp.SocketTable[conn]
	if ok {
		errMsg := fmt.Sprintf("vconnect error: %s: %d has already been connected.\n", conn.RemoteIP, conn.RemotePort)
		tcp.Lock.Unlock()
		return nil, errors.New(errMsg)
	}

	// make the other socket. set its state to SYN_SENT because we'll be sending a syn.
	socket, err := MakeTCPSocket(SYN_SENT, &conn, 0)
	if err != nil {
		tcp.Lock.Unlock()
		return nil, err
	}

	tcp.SocketTable[conn] = socket
	tcp.Lock.Unlock()

	fmt.Println("vconnect socket: initial seq number", socket.InitSeqNum)
	fmt.Println("vconnect socket: initial ack number", socket.RemoteISN)

	tcpheader := header.TCPFields{
		SrcPort:       conn.LocalPort,
		DstPort:       conn.RemotePort,
		SeqNum:        socket.InitSeqNum,
		AckNum:        0,
		DataOffset:    TCPHeaderLen,
		Flags:         header.TCPFlagSyn,
		WindowSize:    MaxBufferSize,
		Checksum:      0,
		UrgentPointer: 0,
	}

	synPacket := TCPPacket{
		SrcIP:  tcp.LocalIP,
		Header: tcpheader,
		Data:   make([]byte, 0),
	}

	synPacketBytes := synPacket.Marshal()

	// send the syn packet
	err = tcp.IPStack.SendIP(remoteIPAddr, TCPProtocolNum, synPacketBytes)
	if err != nil {
		return nil, err
	}
	fmt.Println("1. sent syn, ", synPacket.Header.SeqNum, ", ", synPacket.Header.AckNum)
	socket.ConnState = SYN_SENT

	// expects a syn-ack flag in return, once we have it send a ack packet
	select {
	case packet := <-socket.PacketChannel:
		if packet.Header.Flags != header.TCPFlagAck|header.TCPFlagSyn {
			return nil, errors.New("vconnect: did not receive SYN-ACK flag. received other flag")
		} else if packet.Header.AckNum != socket.InitSeqNum+1 {
			return nil, errors.New("wrong ack number received")
		}

		socket.RemoteISN = packet.Header.SeqNum
		socket.NextExpectedByte = 1
		socket.LargestAck = packet.Header.AckNum

		fmt.Println("4. received syn-ack, ", packet.Header.SeqNum, ", ", packet.Header.AckNum)
		tcpheader = header.TCPFields{
			SrcPort:       conn.LocalPort,
			DstPort:       conn.RemotePort,
			SeqNum:        packet.Header.AckNum,
			AckNum:        packet.Header.SeqNum + 1,
			DataOffset:    TCPHeaderLen,
			Flags:         header.TCPFlagAck,
			WindowSize:    uint16(MaxBufferSize),
			Checksum:      0,
			UrgentPointer: 0,
		}

		ackPacket := TCPPacket{
			SrcIP:  tcp.LocalIP,
			Header: tcpheader,
			Data:   make([]byte, 0),
		}
		ackPacketBytes := ackPacket.Marshal()

		// send the ack packet
		err = tcp.IPStack.SendIP(remoteIPAddr, TCPProtocolNum, ackPacketBytes)
		// TODO: If err is not nil after sending, delete the conn.
		if err != nil {
			return nil, err
		}
		fmt.Println("5. sent ack, ", ackPacket.Header.SeqNum, ", ", ackPacket.Header.AckNum)

		// after sending the ack packet, set the socket state to ESTABLISHED.
		socket.ConnState = ESTABLISHED
		socket.RemoteWindowSize = packet.Header.WindowSize

		// TODO: handle reading and writing next
		go socket.HandleConn()
		return &conn, nil
	case <-socket.CloseSocket:
		// deleteConnSafe
		tcp.Lock.Lock()
		delete(tcp.SocketTable, conn)
		tcp.Lock.Unlock()
		return nil, errors.New("error: closing")

	}
}

func (tcpConn *VTCPConn) VRead(buf []byte) (int, error) {
	// TODO: stop read when conn is closing
	socket, found := tcp.SocketTable[*tcpConn]
	if !found {
		return 0, errors.New("socket not found")
	}

	if socket.ConnState > ESTABLISHED {
		return 0, errors.New("socket is closing")
	}

	for {
		socket.ReadLock.Lock()
		if !socket.ReadBuffer.IsEmpty() {
			bytesRead, err := socket.ReadBuffer.Read(buf)

			socket.ReadLock.Unlock()
			if err != nil {
				return 0, err
			}
			return bytesRead, nil
		} else if socket.ConnState == ESTABLISHED {
			fmt.Println("waiting to read")
			socket.ReadBufferNotEmpty.Wait()
			socket.ReadLock.Unlock()
		} else {
			socket.ReadLock.Unlock()
			return 0, errors.New("connection is not established")
		}
	}
}

func (tcpConn *VTCPConn) VWrite(data []byte) (int, error) {
	tcpSocket, found := tcp.SocketTable[*tcpConn]
	if !found {
		return 0, errors.New("socket does not exist")
	}

	if tcpSocket.ConnState > ESTABLISHED {
		return 0, errors.New("socket is closing")
	}

	numBytesWritten := 0

	tcpSocket.WriteLock.Lock()
	for numBytesWritten < len(data) {
		if tcpSocket.WriteBuffer.IsFull() {
			tcpSocket.WriteNotFull.Wait()
		}

		bytesWrote, err := tcpSocket.WriteBuffer.Write(data[numBytesWritten:])
		if err != nil && err != ringbuffer.ErrTooManyDataToWrite {
			fmt.Println("vwrite error while writing: ", err)
			tcpSocket.WriteLock.Unlock()
			return numBytesWritten, err
		}

		numBytesWritten += bytesWrote
		tcpSocket.WriteNotEmpty.Broadcast()

	}
	tcpSocket.WriteLock.Unlock()
	return numBytesWritten, nil
}

// Initiates the connection termination process for this socket (equivalent to CLOSE in the RFC).
// This method should be used to indicate that the user is done sending/receiving on this socket.
// Calling VClose should send a FIN, and all subsequent calls to VRead and VWrite on this socket should return an error.
// Any data not yet ACKed should still be retransmitted as normal.
// VClose only initiates the close process–it MUST NOT block.
// Consequently, VClose does not delete sockets, the socket MUST NOT be deleted from the socket table until it has reached the CLOSED state (ie, after the other side closes the connection, or an error occurs).

func (tcpConn *VTCPConn) VClose() error {
	// TODO: stop read/write on close
	tcp.Lock.Lock()
	tcpSocket, found := tcp.SocketTable[*tcpConn]
	tcp.Lock.Unlock()

	if !found {
		return errors.New("socket not found")
	}

	switch tcpSocket.ConnState {
	case CLOSED:
		return errors.New("error: connection does not exist")
	case LISTEN:
		return errors.New("should not happen, because we should not call on listener")
	case SYN_SENT:
		//  return "error: closing" responses to any queued SENDs, or RECEIVEs.
		tcpSocket.CloseSocket <- true
		return nil
	case SYN_RECEIVED:
		//If no SENDs have been issued and there is no pending data to send,
		// then form a FIN segment and send it, and enter FIN-WAIT-1 state;
		// otherwise, queue for processing after entering ESTABLISHED state
		return errors.New("should not be called on a non listener")
	case ESTABLISHED:
		// Queue this until all preceding SENDs have been segmentized,
		// then form a FIN segment and send it. In any case, enter FIN-WAIT-1 state.
		// err := SendFinPacket(tcpSocket)
		// if err != nil {
		// 	return err
		// }
		// if this causes problems might be better to put in mutex
		tcpSocket.ConnState = FIN_WAIT_1
		tcpSocket.WriteNotEmpty.Broadcast()
	case FIN_WAIT_1:
		return errors.New("error: connection closing")
	case FIN_WAIT_2:
		return errors.New("error: connection closing")
	case CLOSE_WAIT:
		// Queue this request until all preceding SENDs have been segmentized;
		// then send a FIN segment, enter LAST-ACK state.
		// err := SendFinPacket(tcpSocket)
		// if err != nil {
		// 	return err
		// }
		// if this causes problems might be better to put in mutex
		tcpSocket.WriteNotEmpty.Broadcast()
		tcpSocket.ConnState = LAST_ACK
	case CLOSING:
		return errors.New("error: connection closing")
	case LAST_ACK:
		return errors.New("error: connection closing")
	case TIME_WAIT:
		return errors.New("error: connection closing")
	default:
		return errors.New("error: state does not exist")
	}

	return nil
}
