package qualityplan

import "container/heap"

// heapItem is one reported result: the cycle sequence number and the status.
type heapItem struct {
	seq    int64
	status Status
	index  int
}

// itemHeap is a max-heap of heapItem ordered by cycle sequence number.
type itemHeap []*heapItem

func (h itemHeap) Len() int { return len(h) }

func (h itemHeap) Less(i, j int) bool { return h[i].seq > h[j].seq }

func (h itemHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *itemHeap) Push(x any) {
	it := x.(*heapItem)
	it.index = len(*h)
	*h = append(*h, it)
}

func (h *itemHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// revBucket stores all live results of one case reported at one frozen
// revision, indexed and ordered by cycle sequence number.
type revBucket struct {
	h   itemHeap
	idx map[int64]*heapItem
}

func newRevBucket() *revBucket {
	return &revBucket{idx: make(map[int64]*heapItem)}
}

func (b *revBucket) add(seq int64, st Status) *heapItem {
	it := &heapItem{seq: seq, status: st}
	b.idx[seq] = it
	heap.Push(&b.h, it)
	return it
}

func (b *revBucket) remove(seq int64) {
	it, ok := b.idx[seq]
	if !ok {
		return
	}
	delete(b.idx, seq)
	heap.Remove(&b.h, it.index)
}

func (b *revBucket) top() *heapItem {
	if len(b.h) == 0 {
		return nil
	}
	return b.h[0]
}
