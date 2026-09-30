package qualityplan

import (
	"math/rand"
	"testing"
)

// refModel 是独立的逐项重算参考实现：不做任何增量维护，
// 每次统计都从完整的周期快照与当前结果逐项重算。
type refModel struct {
	cases  map[string]refCase
	suites map[string][]string
	cycles []*refCycle
	byID   map[string]*refCycle
}

type refCase struct {
	suiteID string
	current string
}

type refCycle struct {
	frozen  map[string]string
	results map[string]Status
}

func newRefModel(cases []CaseInit) *refModel {
	m := &refModel{
		cases:  make(map[string]refCase),
		suites: make(map[string][]string),
		byID:   make(map[string]*refCycle),
	}
	for _, ci := range cases {
		if _, ok := m.cases[ci.CaseID]; ok {
			continue
		}
		m.cases[ci.CaseID] = refCase{suiteID: ci.SuiteID, current: ci.Revision}
		m.suites[ci.SuiteID] = append(m.suites[ci.SuiteID], ci.CaseID)
	}
	return m
}

func (m *refModel) activate(caseID, rev string) {
	c := m.cases[caseID]
	c.current = rev
	m.cases[caseID] = c
}

func (m *refModel) openCycle(caseIDs []string) {
	cy := &refCycle{frozen: make(map[string]string), results: make(map[string]Status)}
	for _, id := range caseIDs {
		if _, dup := cy.frozen[id]; dup {
			continue
		}
		cy.frozen[id] = m.cases[id].current
	}
	m.cycles = append(m.cycles, cy)
}

func (m *refModel) apply(c Corrections) {
	for _, it := range c {
		cy := m.byID[it.CycleID]
		if it.Status == NotRun {
			delete(cy.results, it.CaseID)
		} else {
			cy.results[it.CaseID] = it.Status
		}
	}
}

// Corrections 是 Apply 的批次别名，便于参考实现使用。
type Corrections = []Correction

// effective 重算单个用例的有效结果：冻结 revision 等于当前 revision
// 且结果非 NotRun 的周期中，取创建序号最大者。
func (m *refModel) effective(caseID string) Status {
	cur := m.cases[caseID].current
	best := NotRun
	bestSeq := -1
	for seq, cy := range m.cycles {
		rev, ok := cy.frozen[caseID]
		if !ok || rev != cur {
			continue
		}
		st, ok := cy.results[caseID]
		if !ok {
			continue
		}
		if seq > bestSeq {
			bestSeq = seq
			best = st
		}
	}
	return best
}

func addStatus(counts *[statusCount]int, st Status) { counts[st]++ }

// stats 重算指定范围的统计。
func (m *refModel) stats(cycleID, suiteID *string) Stats {
	var total int
	var counts [statusCount]int
	switch {
	case cycleID == nil && suiteID == nil:
		for id := range m.cases {
			addStatus(&counts, m.effective(id))
			total++
		}
	case cycleID == nil:
		for _, id := range m.suites[*suiteID] {
			addStatus(&counts, m.effective(id))
			total++
		}
	case suiteID == nil:
		cy := m.byID[*cycleID]
		for id := range cy.frozen {
			st, _ := cy.results[id]
			addStatus(&counts, st)
			total++
		}
	default:
		cy := m.byID[*cycleID]
		for id := range cy.frozen {
			if m.cases[id].suiteID != *suiteID {
				continue
			}
			st, _ := cy.results[id]
			addStatus(&counts, st)
			total++
		}
	}
	return makeStats(total, counts)
}

// TestRandomizedCrossCheck 用固定随机种子的操作序列，
// 在每次操作后核对所有层级的五种状态计数与比例。
func TestRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20260929))

	const nCases = 24
	suites := []string{"s1", "s2", "s3"}
	revisions := []string{"r1", "r2", "r3"}
	var inits []CaseInit
	var caseIDs []string
	for i := 0; i < nCases; i++ {
		id := caseName(i)
		inits = append(inits, CaseInit{
			CaseID:   id,
			SuiteID:  suites[rng.Intn(len(suites))],
			Revision: revisions[rng.Intn(len(revisions))],
		})
		caseIDs = append(caseIDs, id)
	}

	p := New(inits)
	m := newRefModel(inits)

	var cycleIDs []string
	statuses := []Status{Pass, Fail, Blocked, Skipped, NotRun}

	// checkAll 核对计划、各套件、各周期、各周期内套件四个层级。
	checkAll := func(step int) {
		t.Helper()
		compare := func(cycleID, suiteID *string) {
			t.Helper()
			got, err := p.Stats(cycleID, suiteID)
			if err != nil {
				t.Fatalf("step %d: Stats error: %v", step, err)
			}
			want := m.stats(cycleID, suiteID)
			if got != want {
				t.Fatalf("step %d: Stats(%v,%v) = %+v, want %+v",
					step, deref(cycleID), deref(suiteID), got, want)
			}
			sum := got.Counts.Pass + got.Counts.Fail + got.Counts.Blocked +
				got.Counts.Skipped + got.Counts.NotRun
			if sum != got.Total {
				t.Fatalf("step %d: counts sum %d != total %d", step, sum, got.Total)
			}
		}
		compare(nil, nil)
		for _, s := range suites {
			compare(nil, ptr(s))
		}
		for _, cid := range cycleIDs {
			compare(ptr(cid), nil)
			for _, s := range suites {
				compare(ptr(cid), ptr(s))
			}
		}
	}

	checkAll(0)
	const steps = 4000
	for step := 1; step <= steps; step++ {
		switch rng.Intn(4) {
		case 0: // Activate
			id := caseIDs[rng.Intn(nCases)]
			rev := revisions[rng.Intn(len(revisions))]
			if err := p.Activate(id, rev); err != nil {
				t.Fatalf("step %d: Activate: %v", step, err)
			}
			m.activate(id, rev)
		case 1: // OpenCycle，随机非空子集
			k := 1 + rng.Intn(8)
			perm := rng.Perm(nCases)[:k]
			members := make([]string, 0, k)
			for _, idx := range perm {
				members = append(members, caseIDs[idx])
			}
			cid := cycleName(len(cycleIDs))
			if err := p.OpenCycle(cid, members); err != nil {
				t.Fatalf("step %d: OpenCycle: %v", step, err)
			}
			m.openCycle(members)
			m.byID[cid] = m.cycles[len(m.cycles)-1]
			cycleIDs = append(cycleIDs, cid)
		default: // Apply 批次，可跨周期
			if len(cycleIDs) == 0 {
				continue
			}
			n := 1 + rng.Intn(6)
			batch := make([]Correction, 0, n)
			used := make(map[[2]string]bool)
			for len(batch) < n {
				cid := cycleIDs[rng.Intn(len(cycleIDs))]
				cid2 := caseIDs[rng.Intn(nCases)]
				key := [2]string{cid, cid2}
				if used[key] {
					continue
				}
				// 只对该周期的成员生成更正，保证合法。
				if _, ok := m.byID[cid].frozen[cid2]; !ok {
					continue
				}
				used[key] = true
				batch = append(batch, Correction{
					CycleID: cid,
					CaseID:  cid2,
					Status:  statuses[rng.Intn(len(statuses))],
				})
			}
			if err := p.Apply(batch); err != nil {
				t.Fatalf("step %d: Apply: %v", step, err)
			}
			m.apply(batch)
		}
		checkAll(step)
	}
}

func caseName(i int) string {
	return "case-" + itoa(i)
}

func cycleName(i int) string {
	return "cycle-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func deref(s *string) string {
	if s == nil {
		return "nil"
	}
	return *s
}
