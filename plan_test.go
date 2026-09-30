package qualityplan

import (
	"math"
	"testing"
)

func checkStats(t *testing.T, label string, got Stats, total, notrun, pass, fail, blocked, skipped int64) {
	t.Helper()
	want := [numStatuses]int64{notrun, pass, fail, blocked, skipped}
	if got.Counts != want {
		t.Fatalf("%s: counts = %v, want %v", label, got.Counts, want)
	}
	if got.Total != total {
		t.Fatalf("%s: total = %d, want %d", label, got.Total, total)
	}
	var sum int64
	for _, n := range got.Counts {
		sum += n
	}
	if sum != got.Total {
		t.Fatalf("%s: counts sum %d != total %d", label, sum, got.Total)
	}
	var passRate, blockedRate, completion float64
	if pass+fail > 0 {
		passRate = float64(pass) / float64(pass+fail)
	}
	if total > 0 {
		blockedRate = float64(blocked) / float64(total)
		completion = float64(pass+fail+skipped) / float64(total)
	}
	if math.Abs(got.PassRate-passRate) > 1e-12 ||
		math.Abs(got.BlockedRate-blockedRate) > 1e-12 ||
		math.Abs(got.CompletionRate-completion) > 1e-12 {
		t.Fatalf("%s: rates = (%v,%v,%v), want (%v,%v,%v)", label,
			got.PassRate, got.BlockedRate, got.CompletionRate,
			passRate, blockedRate, completion)
	}
}

func mustApply(t *testing.T, p *Plan, batch ...Correction) {
	t.Helper()
	if err := p.Apply(batch); err != nil {
		t.Fatalf("Apply(%v): %v", batch, err)
	}
}

func mustActivate(t *testing.T, p *Plan, caseID, rev string) {
	t.Helper()
	if err := p.Activate(caseID, rev); err != nil {
		t.Fatalf("Activate(%s,%s): %v", caseID, rev, err)
	}
}

func planStats(t *testing.T, p *Plan) Stats {
	t.Helper()
	st, err := p.Stats(StatsQuery{})
	if err != nil {
		t.Fatalf("Stats(plan): %v", err)
	}
	return st
}

func cycleStats(t *testing.T, p *Plan, cycleID string) Stats {
	t.Helper()
	st, err := p.Stats(StatsQuery{CycleID: StrPtr(cycleID)})
	if err != nil {
		t.Fatalf("Stats(cycle %s): %v", cycleID, err)
	}
	return st
}

// Two revisions of one case across three cycles, with interleaved rollback,
// late corrections, and withdrawal of the newest result.
func TestRevisionRollbackAndLateCorrections(t *testing.T) {
	p, err := NewPlan(
		map[string]string{"c1": "A", "c2": "A", "c3": "B"},
		map[string]string{"c1": "r1", "c2": "v1", "c3": "v1"},
	)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if err := p.OpenCycle("K1", []string{"c1", "c2", "c3"}); err != nil {
		t.Fatalf("OpenCycle(K1): %v", err)
	}
	mustApply(t, p,
		Correction{"K1", "c1", Pass},
		Correction{"K1", "c2", Pass},
		Correction{"K1", "c3", Fail},
	)
	checkStats(t, "plan after K1 results", planStats(t, p), 3, 0, 2, 1, 0, 0)
	checkStats(t, "K1", cycleStats(t, p, "K1"), 3, 0, 2, 1, 0, 0)

	// c1 now runs r2; K1 results are frozen at r1 and must not leak.
	mustActivate(t, p, "c1", "r2")
	checkStats(t, "plan after activate r2", planStats(t, p), 3, 1, 1, 1, 0, 0)

	if err := p.OpenCycle("K2", []string{"c1", "c2"}); err != nil {
		t.Fatalf("OpenCycle(K2): %v", err)
	}
	mustApply(t, p, Correction{"K2", "c1", Fail})
	checkStats(t, "plan after K2 c1=Fail", planStats(t, p), 3, 0, 1, 2, 0, 0)
	checkStats(t, "K2", cycleStats(t, p, "K2"), 2, 1, 0, 1, 0, 0)

	if err := p.OpenCycle("K3", []string{"c1", "c3"}); err != nil {
		t.Fatalf("OpenCycle(K3): %v", err)
	}
	mustApply(t, p, Correction{"K3", "c1", Blocked})
	checkStats(t, "plan after K3 c1=Blocked", planStats(t, p), 3, 0, 1, 1, 1, 0)

	// Late correction to historical cycle K1 (frozen r1, current r2):
	// plan stats unchanged, K1 cycle stats updated.
	mustApply(t, p, Correction{"K1", "c1", Skipped})
	checkStats(t, "plan after late K1 correction", planStats(t, p), 3, 0, 1, 1, 1, 0)
	checkStats(t, "K1 after late correction", cycleStats(t, p, "K1"), 3, 0, 1, 1, 0, 1)

	// Roll back to r1: the r1 result in K1 becomes the candidate again.
	mustActivate(t, p, "c1", "r1")
	checkStats(t, "plan after rollback to r1", planStats(t, p), 3, 0, 1, 1, 0, 1)
	checkStats(t, "K2 untouched by rollback", cycleStats(t, p, "K2"), 2, 1, 0, 1, 0, 0)
	checkStats(t, "K3 untouched by rollback", cycleStats(t, p, "K3"), 2, 1, 0, 0, 1, 0)

	// One batch rewrites the same case in two cycles (both frozen r2,
	// current r1): plan stats unchanged, cycle stats updated.
	mustApply(t, p,
		Correction{"K2", "c1", Blocked},
		Correction{"K3", "c1", Pass},
	)
	checkStats(t, "plan after cross-cycle batch", planStats(t, p), 3, 0, 1, 1, 0, 1)
	checkStats(t, "K2 after batch", cycleStats(t, p, "K2"), 2, 1, 0, 0, 1, 0)
	checkStats(t, "K3 after batch", cycleStats(t, p, "K3"), 2, 1, 1, 0, 0, 0)

	// Switch forward to r2: newest r2 result (K3=Pass) wins.
	mustActivate(t, p, "c1", "r2")
	checkStats(t, "plan after activate r2 again", planStats(t, p), 3, 0, 2, 1, 0, 0)

	// Withdraw the newest candidate: fall back to the next-newest (K2=Blocked).
	mustApply(t, p, Correction{"K3", "c1", NotRun})
	checkStats(t, "plan after withdrawing K3", planStats(t, p), 3, 0, 1, 1, 1, 0)
	checkStats(t, "K3 after withdrawal", cycleStats(t, p, "K3"), 2, 2, 0, 0, 0, 0)

	// Withdraw the last r2 candidate: the case contributes NotRun.
	mustApply(t, p, Correction{"K2", "c1", NotRun})
	checkStats(t, "plan after withdrawing K2", planStats(t, p), 3, 1, 1, 1, 0, 0)

	// Switching back to r1 re-enables its historical result.
	mustActivate(t, p, "c1", "r1")
	checkStats(t, "plan after final rollback", planStats(t, p), 3, 0, 1, 1, 0, 1)
	checkStats(t, "K1 still frozen in time", cycleStats(t, p, "K1"), 3, 0, 1, 1, 0, 1)

	st, err := p.Stats(StatsQuery{SuiteID: StrPtr("A")})
	if err != nil {
		t.Fatalf("Stats(suite A): %v", err)
	}
	checkStats(t, "suite A", st, 2, 0, 1, 0, 0, 1)
	st, err = p.Stats(StatsQuery{CycleID: StrPtr("K1"), SuiteID: StrPtr("A")})
	if err != nil {
		t.Fatalf("Stats(K1,A): %v", err)
	}
	checkStats(t, "K1 suite A", st, 2, 0, 1, 0, 0, 1)
}

func TestErrors(t *testing.T) {
	p, err := NewPlan(
		map[string]string{"c1": "A"},
		map[string]string{"c1": "r1"},
	)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if _, err := NewPlan(map[string]string{"c1": "A"}, map[string]string{}); err == nil {
		t.Fatal("NewPlan without revision should fail")
	}
	if err := p.Activate("nope", "r1"); err == nil {
		t.Fatal("Activate unknown case should fail")
	}
	if err := p.OpenCycle("K0", nil); err == nil {
		t.Fatal("OpenCycle empty should fail")
	}
	if err := p.OpenCycle("K1", []string{"c1"}); err != nil {
		t.Fatalf("OpenCycle: %v", err)
	}
	if err := p.OpenCycle("K1", []string{"c1"}); err == nil {
		t.Fatal("duplicate cycle should fail")
	}
	if err := p.OpenCycle("K2", []string{"ghost"}); err == nil {
		t.Fatal("OpenCycle unknown case should fail")
	}
	if err := p.Apply([]Correction{{"K1", "c1", Pass}, {"K1", "c1", Fail}}); err == nil {
		t.Fatal("duplicate pair in batch should fail")
	}
	if err := p.Apply([]Correction{{"K9", "c1", Pass}}); err == nil {
		t.Fatal("unknown cycle should fail")
	}
	if err := p.Apply([]Correction{{"K1", "ghost", Pass}}); err == nil {
		t.Fatal("non-member case should fail")
	}
	if err := p.Apply([]Correction{{"K1", "c1", Status(42)}}); err == nil {
		t.Fatal("invalid status should fail")
	}
	if _, err := p.Stats(StatsQuery{CycleID: StrPtr("K9")}); err == nil {
		t.Fatal("Stats unknown cycle should fail")
	}
	if _, err := p.Stats(StatsQuery{SuiteID: StrPtr("Z")}); err == nil {
		t.Fatal("Stats unknown suite should fail")
	}
	// A failed batch must not mutate anything.
	st, err := p.Stats(StatsQuery{CycleID: StrPtr("K1")})
	if err != nil {
		t.Fatalf("Stats(K1): %v", err)
	}
	checkStats(t, "K1 after failed batches", st, 1, 1, 0, 0, 0, 0)
}
