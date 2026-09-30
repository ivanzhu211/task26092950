package qualityplan

import "testing"

func ptr(s string) *string { return &s }

// checkStats 核对指定范围的计数与比例。
func checkStats(t *testing.T, p *Plan, cycleID, suiteID *string, want Stats) {
	t.Helper()
	got, err := p.Stats(cycleID, suiteID)
	if err != nil {
		t.Fatalf("Stats(%v, %v) error: %v", cycleID, suiteID, err)
	}
	if got != want {
		t.Fatalf("Stats(%v, %v) = %+v, want %+v", cycleID, suiteID, got, want)
	}
}

// TestInterleavedRevisions 用同一用例的两个 revision 与三个周期，
// 交错执行回滚、迟到更正与最新结果撤回，核对历史周期不变、当前统计正确回退。
func TestInterleavedRevisions(t *testing.T) {
	// 两个套件：s1 含 c1，s2 含 c2，便于核对套件级统计。
	p := New([]CaseInit{
		{CaseID: "c1", SuiteID: "s1", Revision: "r1"},
		{CaseID: "c2", SuiteID: "s2", Revision: "r1"},
	})

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// 三个周期按顺序创建，均冻结 c1@r1。
	must(p.OpenCycle("cy1", []string{"c1", "c2"}))
	must(p.OpenCycle("cy2", []string{"c1", "c2"}))
	must(p.OpenCycle("cy3", []string{"c1", "c2"}))

	// cy1: c1 Pass, c2 Fail；cy3: c1 Fail（最新）。
	must(p.Apply([]Correction{
		{CycleID: "cy1", CaseID: "c1", Status: Pass},
		{CycleID: "cy1", CaseID: "c2", Status: Fail},
		{CycleID: "cy3", CaseID: "c1", Status: Fail},
	}))

	// 计划级：c1 取最新周期 cy3 的 Fail，c2 取 cy1 的 Fail。
	checkStats(t, p, nil, nil, Stats{
		Total:    2,
		Counts:   Counts{Fail: 2},
		PassRate: 0, BlockedRatio: 0, CompletionRate: 1,
	})
	checkStats(t, p, nil, ptr("s1"), Stats{
		Total: 1, Counts: Counts{Fail: 1}, CompletionRate: 1,
	})

	// 迟到的旧周期更正：cy2 补报 c1 Pass。
	// 因 cy3 序号更大，计划级 c1 仍为 Fail，不得被旧周期盖过。
	must(p.Apply([]Correction{{CycleID: "cy2", CaseID: "c1", Status: Pass}}))
	checkStats(t, p, nil, nil, Stats{
		Total: 2, Counts: Counts{Fail: 2}, CompletionRate: 1,
	})

	// 撤回最新候选（cy3 的 c1）后必须回退到次新候选 cy2 的 Pass。
	must(p.Apply([]Correction{{CycleID: "cy3", CaseID: "c1", Status: NotRun}}))
	checkStats(t, p, nil, nil, Stats{
		Total:    2,
		Counts:   Counts{Pass: 1, Fail: 1},
		PassRate: 0.5, CompletionRate: 1,
	})

	// 历史周期统计只看自身快照，不受上述操作影响。
	checkStats(t, p, ptr("cy1"), nil, Stats{
		Total: 2, Counts: Counts{Pass: 1, Fail: 1}, PassRate: 0.5, CompletionRate: 1,
	})
	checkStats(t, p, ptr("cy2"), nil, Stats{
		Total: 2, Counts: Counts{Pass: 1, NotRun: 1}, PassRate: 1, CompletionRate: 0.5,
	})
	checkStats(t, p, ptr("cy3"), nil, Stats{
		Total: 2, Counts: Counts{NotRun: 2},
	})
	checkStats(t, p, ptr("cy2"), ptr("s1"), Stats{
		Total: 1, Counts: Counts{Pass: 1}, PassRate: 1, CompletionRate: 1,
	})

	// 切换 c1 到 r2：三个周期均冻结 r1，无候选，计划级回退为 NotRun。
	must(p.Activate("c1", "r2"))
	checkStats(t, p, nil, nil, Stats{
		Total: 2, Counts: Counts{Fail: 1, NotRun: 1}, CompletionRate: 0.5,
	})
	// 周期统计不受 Activate 影响。
	checkStats(t, p, ptr("cy2"), nil, Stats{
		Total: 2, Counts: Counts{Pass: 1, NotRun: 1}, PassRate: 1, CompletionRate: 0.5,
	})

	// 在 r2 下开启 cy4 并上报 Blocked；再开一个批次跨周期同时改 c1 的多个周期。
	must(p.OpenCycle("cy4", []string{"c1"}))
	must(p.Apply([]Correction{
		{CycleID: "cy4", CaseID: "c1", Status: Blocked},
		{CycleID: "cy1", CaseID: "c1", Status: Skipped}, // 迟到更正，冻结 r1，不影响当前
		{CycleID: "cy2", CaseID: "c1", Status: NotRun},  // 撤回 r1 的候选，不影响当前
	}))
	checkStats(t, p, nil, nil, Stats{
		Total: 2, Counts: Counts{Fail: 1, Blocked: 1},
		BlockedRatio: 0.5, CompletionRate: 0.5,
	})
	// cy1 快照被更正为 Skipped（历史周期内容可变，但归属仍是 r1）。
	checkStats(t, p, ptr("cy1"), nil, Stats{
		Total: 2, Counts: Counts{Skipped: 1, Fail: 1}, CompletionRate: 1,
	})

	// 切回历史 revision r1：既有结果重新启用。
	// r1 下候选：cy1 Skipped、cy3 已撤回、cy2 已撤回 -> 取 cy1 的 Skipped。
	must(p.Activate("c1", "r1"))
	checkStats(t, p, nil, nil, Stats{
		Total: 2, Counts: Counts{Skipped: 1, Fail: 1}, CompletionRate: 1,
	})
	checkStats(t, p, nil, ptr("s1"), Stats{
		Total: 1, Counts: Counts{Skipped: 1}, CompletionRate: 1,
	})

	// 再次撤回 cy1 的 c1 后，r1 下无候选，回退为 NotRun。
	must(p.Apply([]Correction{{CycleID: "cy1", CaseID: "c1", Status: NotRun}}))
	checkStats(t, p, nil, nil, Stats{
		Total: 2, Counts: Counts{Fail: 1, NotRun: 1}, CompletionRate: 0.5,
	})
}
