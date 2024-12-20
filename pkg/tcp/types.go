package tcp

import (
	"container/heap"
	"ipp/pkg/protocol"
	"math/rand"
	"net/netip"
	"sync"
	"time"

	"github.com/google/netstack/tcpip/header"
	"github.com/smallnest/ringbuffer"
)

type TCPState int

const (
	CLOSED TCPState = iota
	LISTEN
	SYN_SENT
	SYN_RECEIVED
	ESTABLISHED
	FIN_WAIT_1
	FIN_WAIT_2
	CLOSE_WAIT
	CLOSING
	LAST_ACK
	TIME_WAIT
	TCPHeaderLen   = header.TCPMinimumSize
	TCPProtocolNum = uint8(header.TCPProtocolNumber)
	MaxBufferSize  = 1 << 16 -1
	MaxSegmentSize = 1024
	MaxFileSize = 10000000

	ALPHA  float64 = .8
	BETA   float64 = 1.3
	RTOMin float64 = float64(10 * time.Microsecond)
	RTOMax float64 = float64(1 * time.Millisecond)

	MSL time.Duration = 30 * time.Second
)

var (
	socketIDCounter uint32 = 0
	socketIDMutex   sync.Mutex
)

type TCPLayer struct {
	SocketTable    map[VTCPConn]*TCPSocket
	ListenersTable map[uint16]*VTCPListener
	LocalIP        netip.Addr
	UsedPorts      map[uint16]bool
	IPStack        protocol.IPStack
	Lock           sync.Mutex
}

type VTCPListener struct {
	SocketID      uint32
	IPAddress     netip.Addr
	PortNumber    uint16
	PacketChannel chan *TCPPacket
	StopFlag      chan bool
}

type TCPPacket struct {
	SrcIP  netip.Addr
	Header header.TCPFields
	Data   []byte
}

type VTCPConn struct {
	LocalIP    netip.Addr
	LocalPort  uint16
	RemoteIP   netip.Addr
	RemotePort uint16
}

type TCPSocket struct {
	SocketID  uint32
	ConnState TCPState
	Conn      *VTCPConn

	PacketChannel chan *TCPPacket

	// Read and Write Buffers
	ReadBuffer         *ringbuffer.RingBuffer
	ReadBufferNotEmpty *sync.Cond
	ReadLock           *sync.Mutex

	SRTT           float64
	RTO            float64
	RTTimer        time.Timer
	OngoingRTTimer bool

	EarlyArrivalQueue      PriorityQueue
	EarlyArrivalSeqNumSet  map[uint32]bool
	EarlyArrivalQueueLock  *sync.Mutex
	EarlyArrivalPacketSize uint32

	WriteBuffer   *ringbuffer.RingBuffer
	WriteNotFull  *sync.Cond
	WriteNotEmpty *sync.Cond
	WriteLock     *sync.Mutex

	RTQueue         []TCPSegmentItem // in flight queue
	RTQueueLock     *sync.Mutex
	RTQueueNotEmpty *sync.Cond
	BytesInFlight   uint16
	ZWPStop         chan bool
	ZWPMode         bool
	ZWPSeq          uint32
	// Sequence Numbers
	InitSeqNum       uint32
	NumberBytesSent  uint32
	NextExpectedByte uint32 // + RemoteISN = RCR.NXT (relative)

	RemoteISN                 uint32
	LargestAck                uint32 // largest acknowledged sequence number SND.UNA
	RemoteWindowSize          uint16 // SND.WND
	NextSequenceNumber        uint32 // SND.NXT
	LastSegmentSequenceNumber uint32 // SND.WL1 last segment used to update SND.WND (relative)
	LastSegmentAckNumber      uint32 // SND.WL2 last segment used to update SND.WND
	WindowNotEmptyFlag        *sync.Cond

	// Mutex Lock for NextExpectedByte and Buffers
	SocketLock *sync.Mutex

	IsClosed      bool
	CloseSocket   chan bool
	TeardownStarted bool
}

func MakeTCPSocket(connState TCPState, tcpConn *VTCPConn, remoteInitSeqNum uint32) (*TCPSocket, error) {
	socket := TCPSocket{
		SocketID:  getNextSocketID(),
		ConnState: connState,
		Conn:      tcpConn,

		IsClosed: false,

		PacketChannel: make(chan *TCPPacket),
		CloseSocket:   make(chan bool),



		// Read and Write Buffers
		ReadBuffer:  ringbuffer.New(MaxBufferSize),
		ReadLock:    &sync.Mutex{},
		WriteBuffer: ringbuffer.New(MaxBufferSize),
		WriteLock:   &sync.Mutex{},

		SRTT:           float64(RTOMin),
		RTO:            float64(RTOMin),
		OngoingRTTimer: false,
		RTTimer:        *time.NewTimer(time.Duration(0)),

		EarlyArrivalSeqNumSet:  make(map[uint32]bool),
		EarlyArrivalQueueLock:  &sync.Mutex{},
		EarlyArrivalPacketSize: 0,

		RTQueue:       make([]TCPSegmentItem, 0),
		RTQueueLock:   &sync.Mutex{},
		BytesInFlight: 0,
		ZWPStop:       make(chan bool),
		ZWPSeq:        0,

		// Seuqence Numbers
		InitSeqNum:       rand.Uint32(),
		NumberBytesSent:  0,
		NextExpectedByte: 0,

		RemoteISN:                 remoteInitSeqNum,
		LargestAck:                0,
		RemoteWindowSize:          0,
		LastSegmentSequenceNumber: remoteInitSeqNum,
		LastSegmentAckNumber:      0,

		// Mutex Lock for NextExpectedByte
		SocketLock: &sync.Mutex{},
	}

	// socket.NumberBytesSent.Store(uint32(0))
	// socket.EarlyArrivalPacketSize.Store(uint32(0))
	// socket.PacketInFlightSize.Store(uint32(0))
	// socket.NextExpectedByte.Store(uint32(0))
	// socket.LargestAcknowledgedNum.Store(uint32(0))
	// socket.RemoteWindowSize.Store(uint32(0))
	heap.Init(&socket.EarlyArrivalQueue)
	socket.ReadBufferNotEmpty = sync.NewCond(socket.ReadLock)
	socket.WriteNotFull = sync.NewCond(socket.WriteLock)
	socket.WriteNotEmpty = sync.NewCond(socket.WriteLock)
	socket.NextSequenceNumber = socket.InitSeqNum + 1

	socket.RTQueueNotEmpty = sync.NewCond(socket.RTQueueLock)

	return &socket, nil
}

// func (socket *TCPSocket) getAckHeader() *header.TCPFields {
// 	return &header.TCPFields{
// 		SrcPort: socket.Conn.LocalPort,
// 		DstPort: socket.Conn.RemotePort,
// 		SeqNum:  socket.InitSeqNum + socket.NumberBytesSent + 1,
// 		// convert to absolute next expected byte:
// 		AckNum:     socket.NextExpectedByte + socket.RemoteISN,
// 		DataOffset: TCPHeaderLen,
// 		Flags:      header.TCPFlagAck,
// 		WindowSize: uint16(socket.ReadBuffer.Free()),
// 		// compute these later:
// 		Checksum:      0,
// 		UrgentPointer: 0,
// 	}
// }

/* APPENDIX
uint is an unsigned integer type, meaning it can only represent
 non-negative values (zero and positive). */
