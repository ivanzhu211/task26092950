package qualityplan

import "container/heap"

// seqHeap 是周期序号的最大堆，配合成员集合保证每个序号至多入堆一次。
// 堆中允许存在已撤回的惰性条目，取堆顶时弹出并清理。
type seqHeap struct {
	items  []int
	inHeap map[int]struct{}
}

func newSeqHeap() *seqHeap {
	return &seqHeap{inHeap: make(map[int]struct{})}
}

// push 仅在序号不在堆中时入堆，保证堆大小不超过不同序号的个数。
func (h *seqHeap) push(seq int) {
	if _, ok := h.inHeap[seq]; ok {
		return
	}
	h.inHeap[seq] = struct{}{}
	heap.Push(h, seq)
}

// top 返回堆中满足 live 的最大序号；惰性弹出失效条目。无满足项时返回 false。
func (h *seqHeap) top(live map[int]Status) (int, bool) {
	for len(h.items) > 0 {
		seq := h.items[0]
		if _, ok := live[seq]; ok {
			return seq, true
		}
		heap.Pop(h)
		delete(h.inHeap, seq)
	}
	return 0, false
}

func (h *seqHeap) Len() int { return len(h.items) }

func (h *seqHeap) Less(i, j int) bool { return h.items[i] > h.items[j] }

func (h *seqHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *seqHeap) Push(x any) { h.items = append(h.items, x.(int)) }

func (h *seqHeap) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	h.items = old[:n-1]
	return item
}
