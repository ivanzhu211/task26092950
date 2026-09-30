package qualityplan

import (
	"errors"
	"fmt"
)

var (
	ErrUnknownCase     = errors.New("qualityplan: unknown case")
	ErrUnknownCycle    = errors.New("qualityplan: unknown cycle")
	ErrUnknownSuite    = errors.New("qualityplan: unknown suite")
	ErrDuplicateCycle  = errors.New("qualityplan: duplicate cycle id")
	ErrDuplicatePair   = errors.New("qualityplan: batch repeats a (cycle, case) pair")
	ErrEmptyCycle      = errors.New("qualityplan: cycle must contain at least one case")
	ErrNotMember       = errors.New("qualityplan: case is not a member of the cycle")
	ErrInvalidStatus   = errors.New("qualityplan: invalid status")
	ErrMissingRevision = errors.New("qualityplan: case has no initial revision")
)

type caseInfo struct {
	suiteID string
	curRev  string
	buckets map[string]*revBucket
	curSeq  int64
	curSt   Status
}

type suiteInfo struct {
	total  int64
	counts counters
}

type cycleSuite struct {
	total  int64
	counts counters
}

type cycleInfo struct {
	seq    int64
	frozen map[string]string
	counts counters
	suites map[string]*cycleSuite
}

// Plan is a version-aware execution summary for a single quality plan.
// It is not safe for concurrent use.
type Plan struct {
	cases   map[string]*caseInfo
	suites  map[string]*suiteInfo
	cycles  map[string]*cycleInfo
	plan    counters
	nextSeq int64
}

// NewPlan builds a plan from a fixed case-to-suite mapping and the current
// revision of each case.
func NewPlan(caseSuites map[string]string, caseRevisions map[string]string) (*Plan, error) {
	p := &Plan{
		cases:   make(map[string]*caseInfo, len(caseSuites)),
		suites:  make(map[string]*suiteInfo),
		cycles:  make(map[string]*cycleInfo),
		nextSeq: 1,
	}
	for id, suite := range caseSuites {
		rev, ok := caseRevisions[id]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingRevision, id)
		}
		p.cases[id] = &caseInfo{
			suiteID: suite,
			curRev:  rev,
			buckets: make(map[string]*revBucket),
			curSeq:  -1,
		}
		su := p.suites[suite]
		if su == nil {
			su = &suiteInfo{}
			p.suites[suite] = su
		}
		su.total++
		su.counts[NotRun]++
		p.plan[NotRun]++
	}
	return p, nil
}

// topOf returns the newest live result in a bucket, or (-1, NotRun).
func topOf(b *revBucket) (int64, Status) {
	if b == nil {
		return -1, NotRun
	}
	if it := b.top(); it != nil {
		return it.seq, it.status
	}
	return -1, NotRun
}

// setCandidate replaces the result a case contributes to plan and suite
// statistics. seq < 0 means the case contributes NotRun.
func (p *Plan) setCandidate(ci *caseInfo, seq int64, st Status) {
	old, nw := NotRun, NotRun
	if ci.curSeq >= 0 {
		old = ci.curSt
	}
	if seq >= 0 {
		nw = st
	}
	if old != nw {
		p.plan.move(old, nw)
		p.suites[ci.suiteID].counts.move(old, nw)
	}
	ci.curSeq = seq
	ci.curSt = st
}

// Activate switches the current revision of a case, possibly back to a
// historical revision.
func (p *Plan) Activate(caseID, revision string) error {
	ci, ok := p.cases[caseID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	if ci.curRev == revision {
		return nil
	}
	ci.curRev = revision
	seq, st := topOf(ci.buckets[revision])
	p.setCandidate(ci, seq, st)
	return nil
}

// OpenCycle creates an execution cycle for a non-empty set of cases,
// freezing each member's current revision. Cycles receive strictly
// increasing sequence numbers in creation order.
func (p *Plan) OpenCycle(cycleID string, caseIDs []string) error {
	if len(caseIDs) == 0 {
		return ErrEmptyCycle
	}
	if _, ok := p.cycles[cycleID]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateCycle, cycleID)
	}
	for _, id := range caseIDs {
		if _, ok := p.cases[id]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownCase, id)
		}
	}
	cy := &cycleInfo{
		seq:    p.nextSeq,
		frozen: make(map[string]string, len(caseIDs)),
		suites: make(map[string]*cycleSuite),
	}
	p.nextSeq++
	for _, id := range caseIDs {
		if _, dup := cy.frozen[id]; dup {
			continue
		}
		ci := p.cases[id]
		cy.frozen[id] = ci.curRev
		cs := cy.suites[ci.suiteID]
		if cs == nil {
			cs = &cycleSuite{}
			cy.suites[ci.suiteID] = cs
		}
		cs.total++
		cs.counts[NotRun]++
		cy.counts[NotRun]++
	}
	p.cycles[cycleID] = cy
	return nil
}

// Apply applies a batch of corrections. A batch may span cycles but must
// not repeat the same (cycle, case) pair. The batch is validated as a
// whole before anything is applied.
func (p *Plan) Apply(batch []Correction) error {
	type pair struct {
		cycle string
		cse   string
	}
	seen := make(map[pair]struct{}, len(batch))
	for _, c := range batch {
		if !c.Status.valid() {
			return fmt.Errorf("%w: %d", ErrInvalidStatus, int(c.Status))
		}
		cy, ok := p.cycles[c.CycleID]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownCycle, c.CycleID)
		}
		if _, ok := cy.frozen[c.CaseID]; !ok {
			return fmt.Errorf("%w: cycle %s case %s", ErrNotMember, c.CycleID, c.CaseID)
		}
		key := pair{c.CycleID, c.CaseID}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: cycle %s case %s", ErrDuplicatePair, c.CycleID, c.CaseID)
		}
		seen[key] = struct{}{}
	}
	for _, c := range batch {
		p.applyOne(c)
	}
	return nil
}

func (p *Plan) applyOne(c Correction) {
	cy := p.cycles[c.CycleID]
	ci := p.cases[c.CaseID]
	rev := cy.frozen[c.CaseID]
	cs := cy.suites[ci.suiteID]
	b := ci.buckets[rev]
	var old *heapItem
	if b != nil {
		old = b.idx[cy.seq]
	}

	if c.Status == NotRun {
		if old == nil {
			return
		}
		cy.counts.move(old.status, NotRun)
		cs.counts.move(old.status, NotRun)
		b.remove(cy.seq)
		if len(b.idx) == 0 {
			delete(ci.buckets, rev)
		}
		if rev == ci.curRev && ci.curSeq == cy.seq {
			seq, st := topOf(ci.buckets[rev])
			p.setCandidate(ci, seq, st)
		}
		return
	}

	if old != nil {
		if old.status == c.Status {
			return
		}
		cy.counts.move(old.status, c.Status)
		cs.counts.move(old.status, c.Status)
		old.status = c.Status
		if rev == ci.curRev && ci.curSeq == cy.seq {
			p.setCandidate(ci, cy.seq, c.Status)
		}
		return
	}

	if b == nil {
		b = newRevBucket()
		ci.buckets[rev] = b
	}
	b.add(cy.seq, c.Status)
	cy.counts.move(NotRun, c.Status)
	cs.counts.move(NotRun, c.Status)
	if rev == ci.curRev && cy.seq > ci.curSeq {
		p.setCandidate(ci, cy.seq, c.Status)
	}
}

// Stats returns status counts and ratios for the selected scope: the whole
// plan, one suite, one cycle, or one suite within one cycle.
func (p *Plan) Stats(q StatsQuery) (Stats, error) {
	if q.CycleID != nil {
		cy, ok := p.cycles[*q.CycleID]
		if !ok {
			return Stats{}, fmt.Errorf("%w: %s", ErrUnknownCycle, *q.CycleID)
		}
		if q.SuiteID != nil {
			cs, ok := cy.suites[*q.SuiteID]
			if !ok {
				if _, known := p.suites[*q.SuiteID]; !known {
					return Stats{}, fmt.Errorf("%w: %s", ErrUnknownSuite, *q.SuiteID)
				}
				return Stats{}, nil
			}
			return makeStats(cs.counts, cs.total), nil
		}
		return makeStats(cy.counts, int64(len(cy.frozen))), nil
	}
	if q.SuiteID != nil {
		su, ok := p.suites[*q.SuiteID]
		if !ok {
			return Stats{}, fmt.Errorf("%w: %s", ErrUnknownSuite, *q.SuiteID)
		}
		return makeStats(su.counts, su.total), nil
	}
	return makeStats(p.plan, int64(len(p.cases))), nil
}
