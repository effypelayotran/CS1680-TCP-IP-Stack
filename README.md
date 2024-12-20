# README for TCP & IP Projects

# Transmission Control Protocol (TCP)

### Design Choices
We implemented a TCPLayer struct that holds the main TCP state. This Includes socket table, listener table,  and IP stack reference.

Socket-Connection Abstraction---
The TCPSocket struct encapsulates the actual socket state machine and data flow. It holds the:
- TCP state machine
- Send/receive buffers
- Sequence numbers for tracking data flow
- RTT for congestion control
This separation of connection identification (VTCPConn) and socket state (TCPSocket) allows multiple socket endpoints to share the same underlying connection.

The TCPConn struct represents a unique TCP socket connection. It encapsulates the addressing information needed to identify the connection - local and remote IP and port numbers. This allows socket operations to be written generically in terms of a VTCPConn instance without handling low-level packet headers.


Sequence Number Tracking---
TCP sequence numbers and acknowledgement numbers can be tricky to reason about. A design choice was made to track them as absolute values in the TCPSocket struct, instead of relative offsets:
- InitSeqNum: Initial random sequence number
- NextExpectedByte: Absolute seq num of next byte expected
- NumberBytesSent: Amount of data sent so far
This makes the bookkeeping intuitive during data sends, retransmissions etc.


Packet Delivery---
In order to ensure arbitrary delays in packet delivery do not block main application threads, the design incorporates:
Goroutine per socket to handle inbound/outbound packets
Channels to pass packets & status between goroutines
This asynchronous I/O model prevents slow networks from hurting latency.


How In-Flight Packets are Handled---
A priority queue RTQueue holds sent but unacknowledged packets along with metadata like sequence number and send timestamp. 
A timer RTTimer is used to timeout and resend a packet if ACK is not received within estimated RTT.
The BytesInFlight field tracks the total bytes sent but not yet acknowledged. This is used to make congestion control decisions on window size.
As ACKs come back indicating data received, packets are removed from the retransmission queue. If RTTimer expires, the earliest unACKed packet is resent.
The RemoteWindowSize field tracks receiver window size over time based on ACKs. This determines the send rate to avoid overwhelming the receiver.
Zero Window Probing: In case the receiver advertises a 0 window, a keep-alive packet is sent periodically to detect when the window opens up again.


Read and Write Buffers---
The read and write buffers are implemented using a ring buffer from an external library instead of directly using slices. This provides the following benefits:
Handles concurrency control and synchronization
Abstracts away size limits, overflow handling etc.
Easier to swap out later if needed

### Measuring Performance
It took out implementation 0.564 seconds to send and receive 1MB file (big.txt which is exactly 1006738 bytes) on the linear-r1h2 network. It took the reference 0.499 seconds. These times are within the same magnitude.

### Packet Capture
- 3-way handshake. Works as expected.
<img width="1319" alt="Screenshot 2023-11-29 at 10 49 19 PM" src="https://github.com/brown-cs1680-f23/iptcp-ipp/assets/98721353/f78ff984-0504-4898-8094-101d899d4afd">

- One segment that is retransmitted. Works as expected.
<img width="1319" alt="Screenshot 2023-11-29 at 10 40 20 PM (1)" src="https://github.com/brown-cs1680-f23/iptcp-ipp/assets/98721353/c36e00ef-7338-4429-b839-d846dc304643">

- One example segment sent and ack'd. Works as expected.
<img width="1319" alt="Screenshot 2023-11-29 at 10 39 58 PM (1)" src="https://github.com/brown-cs1680-f23/iptcp-ipp/assets/98721353/3d778f13-0e3e-44e5-9721-f4c9d6975e7c">

- Connection Teardown. Works as expected.
<img width="1319" alt="Screenshot 2023-11-29 at 10 42 53 PM" src="https://github.com/brown-cs1680-f23/iptcp-ipp/assets/98721353/e9e1840a-2d9f-4eca-88e9-e4a39361314d">
<img width="1319" alt="Screenshot 2023-11-29 at 10 49 29 PM" src="https://github.com/brown-cs1680-f23/iptcp-ipp/assets/98721353/a0b6a963-20c6-45b6-81ad-ee3d626230f8">


### Known Bugs





# Internet Protocol (IP)
### How you build your abstractions for the IP layer and interfaces. What data structures do you have? How do your vhost and vrouter programs interact with your shared IP stack code?

We represent each node in the stack in our protocol.go file with an IPstack struct, containing 3 elements: a NetworkLayer struct, a LinkLayer struct, and a string name.
The link layer file contains the list of interfaces a given node is connected to and a packetChannel that those interfaces are receiving packets on. The network layer struct contains the Forward Table, the Handler Functions, and a list of RIP neighbors for a given node as well as a packetChannel. We created our forward table as a map, mapping “fwd table entry” structs to “fwd table value” structs where the key struct contains the assigned IP and assigned Prefix that identifies a destination node, and the value struct contains the cost, lastupdatedTime, routeType, and either Interface or a NextHop address. When we call vhost, it initializes the network layer and the link layer via the initialized functions called in our  protocol file. When we call vrouter, it also initalizes the network layer and link layer via the initialize functions in our protocol file. vrouter additionally calls our initialize rip function defined in our rip file.

### How we use Go routines
vhost:
The first goroutine created reads from standard input continuously and sends any input on the keyboardChan channel.
The second goroutine pulls packets off the link layer's PacketChannel and passes them to the network layer for handling. This allows concurrent receiving and handling of packets.
The third goroutine reads commands from keyboardChan and handles the CLI input. This keeps the CLI interactive while packets are processed.
Each interface creates its own goroutine to call link.ReceivePacket() concurrently. This allows all interfaces to receive packets at the same time. The WaitGroup coordinates shutting down cleanly.

vrouter:
A goroutine is spawned to handle the periodic RIP route expiry checks. This happens concurrently with other processing.
Another goroutine handles sending periodic RIP update packets. This allows concurrent receiving and handling of packets.
The initial RIP route population and the goroutine launches occur before the main WaitGroup.wait() call. This allows the RIP protocol to initialize and start running in the background while packet processing begins.


### How we process a packet
Parse the IPv4 header from the raw byte buffer to extract fields like src/dst IP, protocol number, etc.
Validate the checksum in the header matches the computed checksum.
Check if the destination IP is one of our local interfaces. If so, pass the payload to the handler for that protocol.
If not a local IP, look up the interface matching the destination IP prefix.
If no match, check the forwarding table for a next hop IP and get the interface for that next hop.
Get the UDP address of the neighbor for the next hop IP on the matched interface.
Decrement TTL and recompute the checksum before sending.
Construct the full packet with new header + payload.
Send the UDP packet out the matching interface to the next hop.

### What are the different parts of your IP stack and what data structures do they use? How do these parts interact (API functions, channels, shared data, etc.)?
There’s the Link Layer that deals with sending and receiving UDP packets. We plan on using a UDP conn object (a socket) so that we can  send and receive UDP packets. We can also use a channel for each interface so that we can send the packet up to the network layer. Each interface (UDP port) has  a neighbors table/directive(provided in the lnx file) where we can reach the node’s neighbors connected to the same subnet.
 
There’s the Network Layer where we look up the destination IP in the node’s forwarding table and decide whether the destination matches our current device, another local device, or figures out the next hop. For our forwarding table, we’ll create a map where the key is a struct to represent a forwarding table entry, containing the IP Prefix and cost) and the value is a struct representing the next hop, containing an Interface or an IP address. Our network layer will have information about the handler functions that are registered to a type of packet.

We use API functions for the User (upper layer) send and receive stuff from the Network Layer. We have a RegisterHandler that populates the map of handler functions where the key is a Protocol# and the value is the respective handlerFunction.


### What fields in the IP packet are read to determine how to forward a packet?
The checksum and TTL field are read to determine whether the packet is valid. We read the destination address field to determine where the packet is for the current host or for another node on the local network.

### What will you do with a packet destined for local delivery (ie, destination IP == your node’s IP)?
We will read the protocol identifier (protocol #) of that packet. If the number of 0, we call the respective OS function for Test Packets. If it’s 200, we call the RIP protocol. The API will handle registering the handler functions (a callback)..

###  What structures will you use to store routing/forwarding information?
We create a struct that represents a forwarding table where we’ll create a map where the key is a struct to represent a forwarding table entry, containing the IP Prefix and cost) and the value is a struct representing the next hop, containing an Interface or an IP address.

### What happens when a link is disabled? (ie, how is forwarding affected)
We should remove that link from the neighbors list in all interfaces that use the link. When we are sending a packet on our local network and the ip address matches on an entry in our forwarding table we also make sure that the matched interface also contains the actual ip address. If not, we drop the packet.


### Known Bugs
Lists invalid socket (port number is not listening) in socket table
Reading from empty buffer
