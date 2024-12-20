package tcp

import (
	"container/heap"
	"fmt"
	"time"

	"github.com/google/netstack/tcpip/header"
)

func (tcpSocket *TCPSocket) HandleConn() {
	go tcpSocket.HandleVWrite()
	go tcpSocket.HandleRT()
	for !tcpSocket.IsClosed {
		packet := <-tcpSocket.PacketChannel
		tcpSocket.HandleTCPPacket(packet)
	}
}

// Write segment into the read buffer
func (tcpSocket *TCPSocket) WriteSegment(packet *TCPPacket) {
	tcpSocket.ReadLock.Lock()
	bytesWrote, err := tcpSocket.ReadBuffer.Write(packet.Data)
	tcpSocket.ReadLock.Unlock()

	if err != nil {
		fmt.Println("HandlePacket: Error while writing to the read buffer", err)
		return
	} else {
		if bytesWrote != len(packet.Data) {
			fmt.Println("HandlePacket: Could not write everything to the read buffer - This should not be happening!!!")
			return
		}
		tcpSocket.NextExpectedByte += uint32(bytesWrote)
		tcpSocket.ReadBufferNotEmpty.Broadcast()
	}
}

func (tcpSocket *TCPSocket) HandleTCPPacket(packet *TCPPacket) {
	tcpSocket.SocketLock.Lock()
	defer tcpSocket.SocketLock.Unlock()

	// could check socket state first
	// acceptability test first

	if packet.Header.Flags == header.TCPFlagAck {
		// If SND.UNA < SEG.ACK =< SND.NXT, then set SND.UNA <- SEG.ACK.
		// Any segments on the retransmission queue that are thereby entirely acknowledged are removed.
		// Users should receive positive acknowledgments for buffers that have been SENT and fully acknowledged
		if (tcpSocket.LargestAck < packet.Header.AckNum) && (packet.Header.AckNum <= tcpSocket.NextSequenceNumber) {
			//fmt.Println("HandlePacket: Updated largest ack ", packet.Header.AckNum-tcpSocket.InitSeqNum)
			tcpSocket.LargestAck = packet.Header.AckNum
			// remove segments from retransmission queue that are thereby entirely acked

			tcpSocket.RTQueueLock.Lock()
			if tcpSocket.OngoingRTTimer {
				//(5.3) When an ACK is received that acknowledges new data, restart the
				// retransmission timer so that it will expire after RTO seconds
				// (for the current value of RTO).
				tcpSocket.RTTimer.Reset(time.Duration(tcpSocket.RTO))

			}

			for len(tcpSocket.RTQueue) > 0 {
				// there might be an issue with empty packets that are in trasmit, like acks of a segment
				if tcpSocket.RTQueue[0].Priority+len(tcpSocket.RTQueue[0].Value.Data) <= int(tcpSocket.LargestAck) {
					// handle fin packets
					if (tcpSocket.RTQueue[0].Value.Header.Flags & header.TCPFlagFin) != 0 {
						fmt.Println("My fin packet has been acked now")
						switch tcpSocket.ConnState {
						case FIN_WAIT_1:
							tcpSocket.ConnState = FIN_WAIT_2
						case LAST_ACK:
							tcpSocket.ConnState = CLOSED
							tcpSocket.IsClosed = true

							tcp.Lock.Lock()
							delete(tcp.SocketTable, *tcpSocket.Conn)
							tcp.Lock.Unlock()

							tcpSocket.RTQueueLock.Unlock()
							return
						}
					}
					var segmentItem TCPSegmentItem

					tcpSocket.BytesInFlight -= uint16(len(tcpSocket.RTQueue[0].Value.Data))
					segmentItem, tcpSocket.RTQueue = dequeue(tcpSocket.RTQueue)
					//fmt.Printf("dropped segment %d  from RT queue\n", segmentItem.Value.Header.SeqNum-tcpSocket.InitSeqNum)
					if !segmentItem.WasRT {
						// update RTO
						rttMeasured := time.Since(segmentItem.SentTime)
						// fmt.Println("rtt ", rttMeasured.Seconds())
						tcpSocket.SRTT = (ALPHA * tcpSocket.SRTT) + ((1 - ALPHA) * float64(rttMeasured))
						tcpSocket.RTO = max(RTOMin, min(BETA*tcpSocket.SRTT, RTOMax))
						// fmt.Println("updating rto to ", time.Duration(tcpSocket.RTO))
					}
				} else {
					break
				}
			}

			if len(tcpSocket.RTQueue) == 0 {
				// wake up existing timer
				if tcpSocket.OngoingRTTimer {
					// fmt.Println("stopping timer bc rt queue empty")
					tcpSocket.RTTimer.Reset(time.Duration(0))
				}
			}
			tcpSocket.RTQueueLock.Unlock()

		} else if packet.Header.AckNum > tcpSocket.NextSequenceNumber {
			// send an ack
			// drop segment
			//fmt.Printf("ack num too high, received ack for %d, Next Sequence Number: %d \n", packet.Header.AckNum, tcpSocket.NextSequenceNumber)
			return
		}

		// else {
		// 	// fmt.Println("handling packet with ACK, ", packet.Header.AckNum-tcpSocket.InitSeqNum)
		// }

		if (tcpSocket.LargestAck <= packet.Header.AckNum) && (packet.Header.AckNum <= tcpSocket.NextSequenceNumber) {
			// If SND.UNA =< SEG.ACK =< SND.NXT, the send window should be updated. I
			// if (SND.WL1 < SEG.SEQ or (SND.WL1 = SEG.SEQ and SND.WL2 =< SEG.ACK)),
			// set SND.WND <- SEG.WND, set SND.WL1 <- SEG.SEQ, and set SND.WL2 <- SEG.ACK.

			// Note that SND.WND is an offset from SND.UNA, that SND.WL1 records the sequence number
			// of the last segment used to update SND.WND, and that SND.WL2 records the acknowledgment
			// number of the last segment used to update SND.WND. The check here prevents using old segments
			// to update the window.
			if (tcpSocket.LastSegmentSequenceNumber < (packet.Header.SeqNum - tcpSocket.RemoteISN)) ||
				((tcpSocket.LastSegmentSequenceNumber == (packet.Header.SeqNum - tcpSocket.RemoteISN)) &&
					tcpSocket.LastSegmentAckNumber <= packet.Header.AckNum) {
				// if tcpSocket.RemoteWindowSize != packet.Header.WindowSize {
				// 	// fmt.Println("HandlePacket: updated window size ", packet.Header.WindowSize)
				// }
				tcpSocket.RemoteWindowSize = packet.Header.WindowSize

				tcpSocket.LastSegmentSequenceNumber = packet.Header.SeqNum - tcpSocket.RemoteISN
				tcpSocket.LastSegmentAckNumber = packet.Header.AckNum

				if tcpSocket.ZWPMode {
					tcpSocket.RTQueueLock.Lock()
					if (tcpSocket.BytesInFlight-1 < tcpSocket.RemoteWindowSize) && (tcpSocket.ZWPSeq < tcpSocket.LargestAck) {
						// fmt.Println("Window has space, Stopping ZWP")
						tcpSocket.RTQueueLock.Unlock()
						tcpSocket.ZWPStop <- true
					} else {
						tcpSocket.RTQueueLock.Unlock()
						// fmt.Printf("zwp packet num is %d,acked is %d\n", tcpSocket.ZWPSeq-tcpSocket.InitSeqNum, tcpSocket.LargestAck-tcpSocket.InitSeqNum)
					}
				}
			}
		}

		// Process the segment text
		if len(packet.Data) == 0 {
			// fmt.Println("no packet data")
			return
		}

		//fmt.Printf("nxt expected seg: %d, received %d\n", tcpSocket.NextExpectedByte, packet.Header.SeqNum-tcpSocket.RemoteISN)

		// ACK for segment within window
		if tcpSocket.NextExpectedByte == (packet.Header.SeqNum - tcpSocket.RemoteISN) {
			if len(packet.Data) <= tcpSocket.ReadBuffer.Free() {
				tcpSocket.WriteSegment(packet)
				// check early arrivals, move up to next continguous part
				for tcpSocket.EarlyArrivalQueue.Len() > 0 {
					if int(tcpSocket.NextExpectedByte) == tcpSocket.EarlyArrivalQueue[0].Priority {
						// next contingous segment
						segmentItem := tcpSocket.EarlyArrivalQueue[0]

						// if (segmentItem.Value.Header.Flags & (header.TCPFlagFin | header.TCPFlagAck)) != 0 {
						// 	if tcpSocket.ConnState == FIN_WAIT_1 {
						// 		tcpSocket.ConnState = TIME_WAIT
						// 		go TimeWait(tcpSocket)
						// 	} else {
						// 		fmt.Println("Never should happen, received fin + ack")
						// 	}
						// } else
						
						if (segmentItem.Value.Header.Flags & header.TCPFlagFin) != 0 {
							fmt.Println("acking fin now")
							switch tcpSocket.ConnState {
							case ESTABLISHED:
								tcpSocket.ConnState = CLOSE_WAIT
								tcpSocket.ReadBufferNotEmpty.Broadcast()
							case FIN_WAIT_1:
								tcpSocket.ConnState = CLOSING
							case FIN_WAIT_2:
								tcpSocket.ConnState = TIME_WAIT
								go TimeWait(tcpSocket)
							}
						} else {
							tcpSocket.WriteSegment(segmentItem.Value)
						}
						heap.Pop(&tcpSocket.EarlyArrivalQueue)
					} else {
						break
					}
				}
			} else {
				// no free space do we need to send anything look at zero window probing?
			}
		} else if tcpSocket.NextExpectedByte < (packet.Header.SeqNum - tcpSocket.RemoteISN) {
			segmentItem := &TCPSegmentItem{
				Priority: int(packet.Header.SeqNum - tcpSocket.RemoteISN),
				Value:    packet,
			}

			heap.Push(&tcpSocket.EarlyArrivalQueue, segmentItem)
		}
		// sending ACK
		//fmt.Printf("SENDING ACK for %d , window size %d\n", tcpSocket.NextExpectedByte, uint16(tcpSocket.ReadBuffer.Free()))
		
		tcpSocket.ReadLock.Lock()
		ackPacket := TCPPacket{
			Header: header.TCPFields{
				SrcPort:       tcpSocket.Conn.LocalPort,
				DstPort:       tcpSocket.Conn.RemotePort,
				SeqNum:        tcpSocket.InitSeqNum + tcpSocket.NumberBytesSent + 1,
				AckNum:        tcpSocket.NextExpectedByte + tcpSocket.RemoteISN,
				DataOffset:    header.TCPMinimumSize,
				Flags:         header.TCPFlagAck,
				WindowSize:    uint16(tcpSocket.ReadBuffer.Free()),
				Checksum:      0,
				UrgentPointer: 0,
			},
			Data: make([]byte, 0),
		}

		ackPacketBytes := ackPacket.Marshal()
		err := tcp.IPStack.SendIP(tcpSocket.Conn.RemoteIP, TCPProtocolNum, ackPacketBytes)

		if err != nil {
			fmt.Println("error sending ACK: ", err)
		}

		tcpSocket.ReadLock.Unlock()

	} else if packet.Header.Flags&header.TCPFlagFin != 0 {
		if tcpSocket.NextExpectedByte == (packet.Header.SeqNum - tcpSocket.RemoteISN) {
			ackFinPacket := TCPPacket{
				Header: header.TCPFields{
					SrcPort:       tcpSocket.Conn.LocalPort,
					DstPort:       tcpSocket.Conn.RemotePort,
					SeqNum:        tcpSocket.InitSeqNum + tcpSocket.NumberBytesSent + 1,
					AckNum:        tcpSocket.RemoteISN + tcpSocket.NextExpectedByte + 1,
					DataOffset:    header.TCPMinimumSize,
					Flags:         header.TCPFlagAck,
					WindowSize:    uint16(tcpSocket.ReadBuffer.Free()),
					Checksum:      0,
					UrgentPointer: 0,
				},
				Data: make([]byte, 0),
			}

			fmt.Println("Received Fin packet, acking")

			ackFinPacketBytes := ackFinPacket.Marshal()
			err := tcp.IPStack.SendIP(tcpSocket.Conn.RemoteIP, TCPProtocolNum, ackFinPacketBytes)

			if err != nil {
				fmt.Println("error sending ACK: ", err)
			}

			switch tcpSocket.ConnState {
			case ESTABLISHED:
				tcpSocket.ConnState = CLOSE_WAIT
				fmt.Println("waking up read")
				tcpSocket.ReadBufferNotEmpty.Broadcast()
			case FIN_WAIT_1:
				if packet.Header.Flags&header.TCPFlagAck != 0 {
					tcpSocket.ConnState = TIME_WAIT
					go TimeWait(tcpSocket)
				} else {
					tcpSocket.ConnState = CLOSING
				}
			case FIN_WAIT_2:
				tcpSocket.ConnState = TIME_WAIT
				go TimeWait(tcpSocket)
			case TIME_WAIT:
				fmt.Println("would restart timer")
			}
		} else if tcpSocket.NextExpectedByte < (packet.Header.SeqNum - tcpSocket.RemoteISN) {
			fmt.Println("early arrival fin packet")
			segmentItem := &TCPSegmentItem{
				Priority: int(packet.Header.SeqNum - tcpSocket.RemoteISN),
				Value:    packet,
			}

			heap.Push(&tcpSocket.EarlyArrivalQueue, segmentItem)

		}
	}
}

func (tcpSocket *TCPSocket) HandleVWrite() {
	for {
		tcpSocket.WriteLock.Lock()
		if tcpSocket.WriteBuffer.IsEmpty() {
			if tcpSocket.ConnState != ESTABLISHED {

				tcpSocket.WriteLock.Unlock()
				err := SendFinPacket(tcpSocket)
				if err != nil {
					fmt.Println(err)
				}
				return
			}

			// fmt.Println("waiting for data to write")
			tcpSocket.WriteNotEmpty.Wait()
			tcpSocket.WriteLock.Unlock()
		} else {
			// amount you can send = (advertised window from receiver - bytes in flight)
			tcpSocket.RTQueueLock.Lock()
			amountCanSend := int(tcpSocket.RemoteWindowSize) - int(tcpSocket.BytesInFlight)
			// fmt.Println("VWRITE: I think I can send", amountCanSend)
			tcpSocket.RTQueueLock.Unlock()

			if amountCanSend > 0 {
				// size to write is NXT
				sizeToWrite := uint32(min(int(tcpSocket.RemoteWindowSize), tcpSocket.WriteBuffer.Length()))
				// fmt.Println("VWRITE: actually sending ", sizeToWrite, "bytes")

				// get all the bytes to send
				if sizeToWrite == 0 {
					tcpSocket.WriteLock.Unlock()
					fmt.Println("HandleVWrite error: sizeToWrite is 0. Shouldn't happen ")
					continue
				}

				payload := make([]byte, sizeToWrite)
				tcpSocket.WriteBuffer.Read(payload)

				// signal waiting vwrite calls that the buffer is no longer full
				tcpSocket.WriteNotFull.Broadcast()
				tcpSocket.WriteLock.Unlock()

				// split bytes into segments, construct tcp packets and send them
				for sizeToWrite > 0 {
					segmentSize := uint32(min(MaxSegmentSize, int(sizeToWrite)))

					packet := TCPPacket{
						SrcIP: tcp.LocalIP,
						Header: header.TCPFields{
							SrcPort:       tcpSocket.Conn.LocalPort,
							DstPort:       tcpSocket.Conn.RemotePort,
							SeqNum:        tcpSocket.InitSeqNum + tcpSocket.NumberBytesSent + 1,
							AckNum:        tcpSocket.NextExpectedByte + tcpSocket.RemoteISN,
							DataOffset:    TCPHeaderLen,
							Flags:         header.TCPFlagAck,
							WindowSize:    uint16(tcpSocket.ReadBuffer.Free()),
							Checksum:      0,
							UrgentPointer: 0},
						Data: payload[:segmentSize],
					}

					packetBytes := packet.Marshal()
					err := tcp.IPStack.SendIP(tcpSocket.Conn.RemoteIP, TCPProtocolNum, packetBytes)
					if err != nil {
						fmt.Println("HandleVWrite error in SendIP: ", err)
						break
					}

					// Add to in flight list
					tcpSocket.RTQueueLock.Lock()
					if !tcpSocket.OngoingRTTimer {
						//(5.1) Every time a packet containing data is sent (including a
						// retransmission), if the timer is not running, start it running
						// so that it will expire after RTO seconds (for the current value
						// of RTO).
						tcpSocket.RTTimer = *time.NewTimer(time.Duration(tcpSocket.RTO))
						tcpSocket.OngoingRTTimer = true
					}

					segmentItem := TCPSegmentItem{
						Priority: int(packet.Header.SeqNum),
						Value:    &packet,
						SentTime: time.Now(),
						WasRT:    false,
					}
					tcpSocket.RTQueue = enqueue(tcpSocket.RTQueue, segmentItem)
					tcpSocket.BytesInFlight += uint16(segmentSize)

					// TODO: track in flight segment data length

					tcpSocket.RTQueueNotEmpty.Broadcast()
					tcpSocket.RTQueueLock.Unlock()

					// fmt.Printf("HandleVWrite:  send %s, seqNum %d\n", packet.Data, packet.Header.SeqNum-tcpSocket.InitSeqNum)
					payload = payload[segmentSize:]
					tcpSocket.NumberBytesSent += segmentSize
					tcpSocket.NextSequenceNumber += segmentSize

					sizeToWrite -= segmentSize

				}
			} else {
				// ZWP mode
				// fmt.Println("VWRITE: Entering ZWP mode")
				tcpSocket.ZWPMode = true
				zwpPayload := make([]byte, 1)
				tcpSocket.WriteBuffer.Read(zwpPayload)
				tcpSocket.WriteNotFull.Broadcast()
				tcpSocket.WriteLock.Unlock()

				zwpPacket := TCPPacket{
					SrcIP: tcp.LocalIP,
					Header: header.TCPFields{
						SrcPort:       tcpSocket.Conn.LocalPort,
						DstPort:       tcpSocket.Conn.RemotePort,
						SeqNum:        tcpSocket.InitSeqNum + tcpSocket.NumberBytesSent + 1,
						AckNum:        tcpSocket.NextExpectedByte + tcpSocket.RemoteISN,
						DataOffset:    TCPHeaderLen,
						Flags:         header.TCPFlagAck,
						WindowSize:    uint16(tcpSocket.ReadBuffer.Free()),
						Checksum:      0,
						UrgentPointer: 0},
					Data: zwpPayload,
				}

				zwpPacketBytes := zwpPacket.Marshal()

				tcpSocket.NumberBytesSent += 1
				tcpSocket.NextSequenceNumber += 1

				tcpSocket.RTQueueLock.Lock()
				// fmt.Println("zwp should never happen")
				// tcpSocket.RTQueueLock.Lock()
				tcpSocket.ZWPSeq = zwpPacket.Header.SeqNum
				tcpSocket.BytesInFlight += 1
				tcpSocket.RTQueueLock.Unlock()

				// fmt.Println("Probing!")
				err := tcp.IPStack.SendIP(tcpSocket.Conn.RemoteIP, TCPProtocolNum, zwpPacketBytes)
				if err != nil {
					fmt.Println("HandleVWrite error in SendIP: ", err)
				}

				// fmt.Printf("ZWPLoop: send %s, seqNum %d\n", zwpPacket.Data, zwpPacket.Header.SeqNum-tcpSocket.InitSeqNum)
				
			ZWPLoop:
				for {
					select {
					case <-tcpSocket.ZWPStop:
						// wake up when there is space, break out of for loop

						tcpSocket.ZWPMode = false
						tcpSocket.RTQueueLock.Lock()
						tcpSocket.BytesInFlight -= 1
						tcpSocket.RTQueueLock.Unlock()
						break ZWPLoop

					default:
						// send packet at a fixed rate
						// fmt.Println("Probing!")
						err := tcp.IPStack.SendIP(tcpSocket.Conn.RemoteIP, TCPProtocolNum, zwpPacketBytes)
						if err != nil {
							fmt.Println("HandleVWrite error in SendIP: ", err)
						}
						time.Sleep(50 * time.Microsecond)
					}
				}
			}
			// fmt.Println("Exited ZWP mode!")

		}
	}
}

func (tcpSocket *TCPSocket) HandleRT() {
	// if you haven't received an ack, send again
	// *Okay to use sleep, time.Ticker to have separate thread trigger an event, like retransmissions
	for !tcpSocket.IsClosed {
		tcpSocket.RTQueueLock.Lock()
		if len(tcpSocket.RTQueue) == 0 {
			// fmt.Println("waiting for Items in rt queue")
			//  (5.2) When all outstanding data has been acknowledged, turn off the retransmission timer.
			tcpSocket.OngoingRTTimer = false
			tcpSocket.RTQueueNotEmpty.Wait()
			tcpSocket.RTQueueLock.Unlock()
			// fmt.Println("done waiting")
		} else {
			if !tcpSocket.OngoingRTTimer {
				fmt.Println("there is not an ongoing timer when there is outstanding data, NEVER SHOULD HAPPEN")
			}

			// fmt.Println("Items in rt queue")

			tcpSocket.RTQueueLock.Unlock()
			<-tcpSocket.RTTimer.C
			// fmt.Println("Timer expired!")
			tcpSocket.RTQueueLock.Lock()
			// if the first
			// (5.4) Retransmit the earliest segment that has not been acknowledged by the TCP receiver.
			if len(tcpSocket.RTQueue) >= 1 {
				packetBytes := tcpSocket.RTQueue[0].Value.Marshal()
				// fmt.Printf("retransmitting %d, largest ack %d\n", tcpSocket.RTQueue[0].Value.Header.SeqNum-tcpSocket.InitSeqNum,
				//	tcpSocket.LargestAck-tcpSocket.InitSeqNum)
				// fmt.Println("retransmitting should never happen")
				// tcpSocket.RTQueueLock.Lock()

				err := tcp.IPStack.SendIP(tcpSocket.Conn.RemoteIP, TCPProtocolNum, packetBytes)
				if err != nil {
					fmt.Println("HandleVWrite error in SendIP: ", err)
				} else {
					tcpSocket.RTQueue[0].WasRT = true
				}

				tcpSocket.RTO = min(tcpSocket.RTO*2, RTOMax)

				tcpSocket.RTTimer = *time.NewTimer(time.Duration(tcpSocket.RTO))
				// fmt.Printf("reseting timer, rto %f\n", time.Duration(tcpSocket.RTO).Seconds())
				tcpSocket.OngoingRTTimer = true

				//(5.7) If the timer expires awaiting the ACK of a SYN segment and the
				// TCP implementation is using an RTO less than 3 seconds, the RTO
				// MUST be re-initialized to 3 seconds when data transmission
				// begins (i.e., after the three-way handshake completes). ???

			}

			tcpSocket.RTQueueLock.Unlock()

		}

		//For any state if the retransmission timeout expires on a segment in the retransmission queue, send the segment at the front of the retransmission queue again, reinitialize the retransmission timer, and return.

	}
}

func SendFinPacket(socket *TCPSocket) error {
	finPacket := TCPPacket{
		Header: header.TCPFields{
			SrcPort:       socket.Conn.LocalPort,
			DstPort:       socket.Conn.RemotePort,
			SeqNum:        socket.InitSeqNum + socket.NumberBytesSent + 1,
			AckNum:        socket.RemoteISN + socket.NextExpectedByte,
			DataOffset:    TCPHeaderLen,
			Flags:         header.TCPFlagFin,
			WindowSize:    uint16(socket.ReadBuffer.Free()),
			Checksum:      0,
			UrgentPointer: 0,
		},
		Data: make([]byte, 0),
	}

	finPacketBytes := finPacket.Marshal()
	err := tcp.IPStack.SendIP(socket.Conn.RemoteIP, TCPProtocolNum, finPacketBytes)

	if err != nil {
		return err
	}
	fmt.Println("fin packet sequence number ", finPacket.Header.SeqNum - socket.InitSeqNum)
	socket.NumberBytesSent++
	socket.NextSequenceNumber++

	socket.RTQueueLock.Lock()
	socket.RTQueue = enqueue(socket.RTQueue, TCPSegmentItem{
		Value:    &finPacket,
		Priority: int(finPacket.Header.SeqNum),
		SentTime: time.Now(),
		WasRT:    false,
	})
	socket.RTQueueLock.Unlock()

	return nil
}

func TimeWait(socket *TCPSocket) {
	time.Sleep(MSL)
	tcp.Lock.Lock()
	fmt.Println("removing socket")
	delete(tcp.SocketTable, *socket.Conn)
	tcp.Lock.Unlock()
}
