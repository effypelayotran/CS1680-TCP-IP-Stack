package tcp

import (
	"time"
)

// Packet with metadata
type TCPSegmentItem struct {
	Index     int        // The Index of the item in the heap
	Priority  int        // The relative seq number of the packet
	Value     *TCPPacket
	SentTime time.Time
	WasRT     bool
	IsZWP     bool
}

// A PriorityQueue implements heap.Interface and holds Items.
type PriorityQueue []*TCPSegmentItem

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, priority so we use greater than here.
	return pq[i].Priority < pq[j].Priority
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

func (pq *PriorityQueue) Push(x any) {
	n := len(*pq)
	item := x.(*TCPSegmentItem)
	item.Index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.Index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// update modifies the priority and value of an Item in the queue.
// func (pq *PriorityQueue) update(item *TCPSegmentItem, Value *TCPPacket, Priority int) {
// 	item.Value = Value
// 	item.Priority = Priority
// 	heap.Fix(pq, item.Index)
// }

func enqueue(queue []TCPSegmentItem, element TCPSegmentItem) []TCPSegmentItem {
	queue = append(queue, element)
	return queue
}

func dequeue(queue []TCPSegmentItem) (TCPSegmentItem, []TCPSegmentItem) {
	element := queue[0]
	if len(queue) == 1 {
		var tmp = []TCPSegmentItem{}
		return element, tmp
	}
	return element, queue[1:]
}
