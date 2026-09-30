package qualityplan

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// refModel is an independent, brute-force implementation. It keeps full
// cycle snapshots and recomputes every statistic from scratch.
type refModel struct {
	caseSuite map[string]string
	curRev    map[string]string
	cycles    map[string]*refCycle
}

type refCycle struct {
	seq     int64
	frozen  map[string]string
	results map[string]Status
}

func newRefModel(caseSuites map[string]string, revisions map[string]string) *refModel {
	m := &refModel{
		caseSuite: make(map[string]string),
		curRev:    make(map[string]string),
		cycles:    make(map[string]*refCycle),
	}
	for id, s := range caseSuites {
		m.caseSuite[id] = s
		m.curRev[id] = revisions[id]
	}
	return m
}

func (m *refModel) activate(caseID, rev string) {
	m.curRev[caseID] = rev
}

func (m *refModel) openCycle(cycleID string, caseIDs []string, seq int64) {
	cy := &refCycle{seq: seq, frozen: make(map[string]string), results: make(map[string]Status)}
	for _, id := range caseIDs {
		if _, dup := cy.frozen[id]; dup {
			continue
		}
		cy.frozen[id] = m.curRev[id]
	}
	m.cycles[cycleID] = cy
}

func (m *refModel) apply(c Correction) {
	cy := m.cycles[c.CycleID]
	if c.Status == NotRun {
		delete(cy.results, c.CaseID)
		return
	}
	cy.results[c.CaseID] = c.Status
}

func (m *refModel) stats(cycleID, suiteID *string) Stats {
	var counts [numStatuses]int64
	var total int64
	if cycleID == nil {
		for id, suite := range m.caseSuite {
			if suiteID != nil && suite != *suiteID {
				continue
			}
			total++
			bestSeq := int64(-1)
			bestSt := NotRun
			for _, cy := range m.cycles {
				rev, member := cy.frozen[id]
				if !member || rev != m.curRev[id] {
					continue
				}
				st, reported := cy.results[id]
				if !reported {
					continue
				}
				if cy.seq > bestSeq {
					bestSeq = cy.seq
					bestSt = st
				}
			}
			counts[bestSt]++
		}
		return makeStats(counts, total)
	}
	cy := m.cycles[*cycleID]
	for id := range cy.frozen {
		if suiteID != nil && m.caseSuite[id] != *suiteID {
			continue
		}
		total++
		st, reported := cy.results[id]
		if !reported {
			st = NotRun
		}
		counts[st]++
	}
	return makeStats(counts, total)
}

func sameStats(t *testing.T, label string, got, want Stats) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %+v, want %+v", label, got, want)
	}
}

func checkAllScopes(t *testing.T, p *Plan, m *refModel, suites []string, cycleIDs []string, op int) {
	t.Helper()
	label := func(scope string) string { return fmt.Sprintf("op %d %s", op, scope) }

	got, err := p.Stats(StatsQuery{})
	if err != nil {
		t.Fatalf("op %d plan stats: %v", op, err)
	}
	sameStats(t, label("plan"), got, m.stats(nil, nil))

	for _, s := range suites {
		got, err := p.Stats(StatsQuery{SuiteID: StrPtr(s)})
		if err != nil {
			t.Fatalf("op %d suite %s stats: %v", op, s, err)
		}
		sameStats(t, label("suite "+s), got, m.stats(nil, &s))
	}

	for _, cid := range cycleIDs {
		got, err := p.Stats(StatsQuery{CycleID: StrPtr(cid)})
		if err != nil {
			t.Fatalf("op %d cycle %s stats: %v", op, cid, err)
		}
		sameStats(t, label("cycle "+cid), got, m.stats(&cid, nil))
		for _, s := range suites {
			got, err := p.Stats(StatsQuery{CycleID: StrPtr(cid), SuiteID: StrPtr(s)})
			if err != nil {
				t.Fatalf("op %d cycle %s suite %s stats: %v", op, cid, s, err)
			}
			sameStats(t, label("cycle "+cid+" suite "+s), got, m.stats(&cid, &s))
		}
	}
}

func TestDifferentialAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20260929))

	const numCases = 16
	suites := []string{"s1", "s2", "s3", "s4"}
	revisions := []string{"r1", "r2", "r3"}
	caseSuites := make(map[string]string)
	caseRevs := make(map[string]string)
	caseIDs := make([]string, 0, numCases)
	for i := 0; i < numCases; i++ {
		id := fmt.Sprintf("case-%d", i)
		caseSuites[id] = suites[rng.Intn(len(suites))]
		caseRevs[id] = revisions[rng.Intn(len(revisions))]
		caseIDs = append(caseIDs, id)
	}

	p, err := NewPlan(caseSuites, caseRevs)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	m := newRefModel(caseSuites, caseRevs)

	var cycleIDs []string
	const numOps = 1500
	for op := 0; op < numOps; op++ {
		switch x := rng.Intn(100); {
		case x < 15 || len(cycleIDs) == 0:
			// OpenCycle with a random non-empty subset.
			n := 1 + rng.Intn(8)
			members := make([]string, 0, n)
			for i := 0; i < n; i++ {
				members = append(members, caseIDs[rng.Intn(len(caseIDs))])
			}
			cid := fmt.Sprintf("cy-%d", len(cycleIDs))
			if err := p.OpenCycle(cid, members); err != nil {
				t.Fatalf("op %d OpenCycle: %v", op, err)
			}
			m.openCycle(cid, members, int64(len(cycleIDs)+1))
			cycleIDs = append(cycleIDs, cid)
		case x < 45:
			// Activate a random case to a random revision.
			id := caseIDs[rng.Intn(len(caseIDs))]
			rev := revisions[rng.Intn(len(revisions))]
			if err := p.Activate(id, rev); err != nil {
				t.Fatalf("op %d Activate: %v", op, err)
			}
			m.activate(id, rev)
		default:
			// Apply a batch of corrections across random cycles.
			totalPairs := 0
			for _, cy := range m.cycles {
				totalPairs += len(cy.frozen)
			}
			maxN := 8
			if totalPairs < maxN {
				maxN = totalPairs
			}
			n := 1 + rng.Intn(maxN)
			batch := make([]Correction, 0, n)
			used := make(map[[2]string]bool)
			for len(batch) < n {
				cid := cycleIDs[rng.Intn(len(cycleIDs))]
				cy := m.cycles[cid]
				members := make([]string, 0, len(cy.frozen))
				for id := range cy.frozen {
					members = append(members, id)
				}
				sort.Strings(members)
				id := members[rng.Intn(len(members))]
				key := [2]string{cid, id}
				if used[key] {
					continue
				}
				used[key] = true
				batch = append(batch, Correction{cid, id, Status(rng.Intn(numStatuses))})
			}
			if err := p.Apply(batch); err != nil {
				t.Fatalf("op %d Apply: %v", op, err)
			}
			for _, c := range batch {
				m.apply(c)
			}
		}

		// Check every scope after every operation: plan, all suites, and
		// all cycles (both whole-cycle and per-suite-within-cycle).
		checkAllScopes(t, p, m, suites, cycleIDs, op)
	}
}
