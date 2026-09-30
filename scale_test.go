package qualityplan

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

func heapAlloc() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestScale exercises the target scale: 100k cases, 1M cycle-case pairs,
// one case with 100k cycles of history, and sustained withdraw/re-report
// churn on a fixed dataset. Run with:
//
//	go test -run TestScale -v .
func TestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test disabled in -short mode")
	}
	const (
		numCases    = 100_000
		numSuites   = 64
		deepCycles  = 100_000 // cycles containing the deep-history case
		wideCycles  = 900
		wideMembers = 1000 // 900*1000 = 900k pairs; +100k deep = 1M pairs
	)
	deepCase := "case-0"

	base := heapAlloc()

	caseSuites := make(map[string]string, numCases)
	caseRevs := make(map[string]string, numCases)
	for i := 0; i < numCases; i++ {
		id := fmt.Sprintf("case-%d", i)
		caseSuites[id] = fmt.Sprintf("suite-%d", i%numSuites)
		caseRevs[id] = fmt.Sprintf("rev-%d", i%3)
	}
	p, err := NewPlan(caseSuites, caseRevs)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}

	start := time.Now()
	for i := 0; i < deepCycles; i++ {
		if err := p.OpenCycle(fmt.Sprintf("deep-%d", i), []string{deepCase}); err != nil {
			t.Fatalf("OpenCycle deep-%d: %v", i, err)
		}
	}
	for j := 0; j < wideCycles; j++ {
		members := make([]string, wideMembers)
		for k := 0; k < wideMembers; k++ {
			members[k] = fmt.Sprintf("case-%d", (j*wideMembers+k)%(numCases-1)+1)
		}
		if err := p.OpenCycle(fmt.Sprintf("wide-%d", j), members); err != nil {
			t.Fatalf("OpenCycle wide-%d: %v", j, err)
		}
	}
	openDur := time.Since(start)

	// Report a result for every one of the 1M cycle-case pairs.
	start = time.Now()
	statuses := []Status{Pass, Pass, Fail, Pass, Blocked, Skipped}
	batch := make([]Correction, 0, 10_000)
	flush := func() {
		if err := p.Apply(batch); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		batch = batch[:0]
	}
	for i := 0; i < deepCycles; i++ {
		batch = append(batch, Correction{fmt.Sprintf("deep-%d", i), deepCase, statuses[i%len(statuses)]})
		if len(batch) == cap(batch) {
			flush()
		}
	}
	for j := 0; j < wideCycles; j++ {
		cid := fmt.Sprintf("wide-%d", j)
		for k := 0; k < wideMembers; k++ {
			id := fmt.Sprintf("case-%d", (j*wideMembers+k)%(numCases-1)+1)
			batch = append(batch, Correction{cid, id, statuses[(j+k)%len(statuses)]})
			if len(batch) == cap(batch) {
				flush()
			}
		}
	}
	flush()
	applyDur := time.Since(start)

	afterLoad := heapAlloc()

	// Invariants: counts sum to the denominator at every level.
	plan, err := p.Stats(StatsQuery{})
	if err != nil {
		t.Fatalf("Stats(plan): %v", err)
	}
	var sum int64
	for _, n := range plan.Counts {
		sum += n
	}
	if plan.Total != numCases || sum != numCases {
		t.Fatalf("plan total=%d sum=%d, want %d", plan.Total, sum, numCases)
	}
	cy, err := p.Stats(StatsQuery{CycleID: StrPtr("wide-0")})
	if err != nil {
		t.Fatalf("Stats(wide-0): %v", err)
	}
	sum = 0
	for _, n := range cy.Counts {
		sum += n
	}
	if cy.Total != wideMembers || sum != wideMembers {
		t.Fatalf("wide-0 total=%d sum=%d, want %d", cy.Total, sum, wideMembers)
	}

	// The deep case's candidate is its newest cycle; withdrawing it must
	// fall back to the next-newest, with no scan over its 100k cycles.
	deepLast := statuses[(deepCycles-1)%len(statuses)]
	deepPrev := statuses[(deepCycles-2)%len(statuses)]
	if plan.Counts[deepLast] == 0 {
		t.Fatalf("expected some %v results", deepLast)
	}
	start = time.Now()
	if err := p.Apply([]Correction{{fmt.Sprintf("deep-%d", deepCycles-1), deepCase, NotRun}}); err != nil {
		t.Fatalf("withdraw deep newest: %v", err)
	}
	withdrawDur := time.Since(start)
	plan2, _ := p.Stats(StatsQuery{})
	if plan2.Counts[deepLast] != plan.Counts[deepLast]-1 ||
		plan2.Counts[deepPrev] != plan.Counts[deepPrev]+1 {
		t.Fatalf("deep fallback wrong: before %v after %v", plan.Counts, plan2.Counts)
	}
	if err := p.Apply([]Correction{{fmt.Sprintf("deep-%d", deepCycles-1), deepCase, deepLast}}); err != nil {
		t.Fatalf("re-report deep newest: %v", err)
	}

	// Stats must be O(1): 1M lookups should take roughly constant time each.
	start = time.Now()
	const statsCalls = 1_000_000
	for i := 0; i < statsCalls; i++ {
		if _, err := p.Stats(StatsQuery{CycleID: StrPtr("wide-500"), SuiteID: StrPtr("suite-5")}); err != nil {
			t.Fatalf("Stats: %v", err)
		}
	}
	statsDur := time.Since(start)

	// Sustained withdraw/re-report churn on a fixed set of positions must
	// not grow memory.
	start = time.Now()
	const churnOps = 500_000
	churn := make([]Correction, 0, 100)
	for i := 0; i < 100; i++ {
		churn = append(churn, Correction{fmt.Sprintf("deep-%d", i*1000), deepCase, NotRun})
	}
	for round := 0; round < churnOps/100; round++ {
		for i := range churn {
			churn[i].Status = NotRun
		}
		if err := p.Apply(churn); err != nil {
			t.Fatalf("churn withdraw: %v", err)
		}
		for i := range churn {
			churn[i].Status = statuses[i%len(statuses)]
		}
		if err := p.Apply(churn); err != nil {
			t.Fatalf("churn re-report: %v", err)
		}
	}
	churnDur := time.Since(start)
	afterChurn := heapAlloc()
	runtime.KeepAlive(p)

	t.Logf("pairs: %d deep + %d wide = %d", deepCycles, wideCycles*wideMembers, deepCycles+wideCycles*wideMembers)
	t.Logf("open %d cycles: %v (%.0f cycles/s)", deepCycles+wideCycles, openDur, float64(deepCycles+wideCycles)/openDur.Seconds())
	t.Logf("apply 1M results: %v (%.0f results/s)", applyDur, 1e6/applyDur.Seconds())
	t.Logf("withdraw newest of 100k-deep case: %v", withdrawDur)
	t.Logf("stats: %v per call over %d calls", statsDur/statsCalls, statsCalls)
	t.Logf("churn %d withdraw+re-report ops: %v (%.0f ops/s)", churnOps, churnDur, float64(churnOps)/churnDur.Seconds())
	t.Logf("heap: base=%dMiB after-load=%dMiB after-churn=%dMiB (churn delta=%dKiB)",
		base>>20, afterLoad>>20, afterChurn>>20, (int64(afterChurn)-int64(afterLoad))>>10)
	if afterChurn > afterLoad+32<<20 {
		t.Fatalf("memory grew by %dMiB during churn on a fixed dataset", (afterChurn-afterLoad)>>20)
	}
}
