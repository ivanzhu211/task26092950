package qualityplan

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// heapAlloc 触发 GC 后返回当前堆分配量。
func heapAlloc() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestScale 覆盖目标数据量：10 万用例、100 万个周期用例对，
// 并报告构建、更正与统计的耗时及内存占用。
// 运行命令：go test -run TestScale -v
func TestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式下跳过规模测试")
	}
	const (
		nCases       = 100_000
		nCycles      = 1_000
		cycleSize    = 1_000 // 每周期成员数，总计 100 万周期用例对
		nActivations = 100_000
		nCorrections = 1_000_000
	)

	inits := make([]CaseInit, nCases)
	for i := range inits {
		inits[i] = CaseInit{
			CaseID:   fmt.Sprintf("case-%d", i),
			SuiteID:  fmt.Sprintf("suite-%d", i%100),
			Revision: "r1",
		}
	}

	base := heapAlloc()
	start := time.Now()
	p := New(inits)
	for c := 0; c < nCycles; c++ {
		members := make([]string, cycleSize)
		for j := range members {
			members[j] = fmt.Sprintf("case-%d", (c*cycleSize+j)%nCases)
		}
		if err := p.OpenCycle(fmt.Sprintf("cycle-%d", c), members); err != nil {
			t.Fatal(err)
		}
	}
	buildDur := time.Since(start)
	buildMem := heapAlloc()
	t.Logf("构建: %d 用例 x %d 周期 = %d 周期用例对, 耗时 %v, 堆内存 %.1f MB",
		nCases, nCycles, nCycles*cycleSize, buildDur, float64(buildMem-base)/1e6)

	// 100 万次更正：确定性伪随机地散布在各周期各用例上。
	start = time.Now()
	seed := uint64(12345)
	next := func() uint64 { seed = seed*6364136223846793005 + 1442695040888963407; return seed >> 11 }
	batch := make([]Correction, 1000)
	for done := 0; done < nCorrections; done += len(batch) {
		for i := range batch {
			c := int(next() % nCycles)
			j := int(next() % cycleSize)
			batch[i] = Correction{
				CycleID: fmt.Sprintf("cycle-%d", c),
				CaseID:  fmt.Sprintf("case-%d", (c*cycleSize+j)%nCases),
				Status:  Status(1 + next()%4), // Pass/Fail/Blocked/Skipped
			}
		}
		// 批次内可能撞车同一 (周期, 用例) 对，去重后提交。
		seen := make(map[[2]string]bool, len(batch))
		uniq := batch[:0]
		for _, c := range batch {
			k := [2]string{c.CycleID, c.CaseID}
			if !seen[k] {
				seen[k] = true
				uniq = append(uniq, c)
			}
		}
		if err := p.Apply(uniq); err != nil {
			t.Fatal(err)
		}
	}
	applyDur := time.Since(start)
	t.Logf("更正: 约 %d 条, 耗时 %v (%.0f 条/秒)", nCorrections, applyDur,
		float64(nCorrections)/applyDur.Seconds())

	// 10 万次 Activate 切换。
	start = time.Now()
	for i := 0; i < nActivations; i++ {
		rev := "r2"
		if i%2 == 0 {
			rev = "r1"
		}
		if err := p.Activate(fmt.Sprintf("case-%d", i%nCases), rev); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("切换: %d 次 Activate, 耗时 %v", nActivations, time.Since(start))

	// 统计必须为 O(1)：重复调用全部四个层级。
	start = time.Now()
	var sum Stats
	for i := 0; i < 10_000; i++ {
		s1, _ := p.Stats(nil, nil)
		s2, _ := p.Stats(nil, ptr("suite-7"))
		s3, _ := p.Stats(ptr("cycle-999"), nil)
		s4, _ := p.Stats(ptr("cycle-999"), ptr("suite-7"))
		sum.Total += s1.Total + s2.Total + s3.Total + s4.Total
	}
	t.Logf("统计: 40000 次 Stats 调用, 耗时 %v (合计 Total=%d)", time.Since(start), sum.Total)

	st, err := p.Stats(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	csum := st.Counts.Pass + st.Counts.Fail + st.Counts.Blocked + st.Counts.Skipped + st.Counts.NotRun
	if csum != st.Total || st.Total != nCases {
		t.Fatalf("计划级计数和 %d != 分母 %d", csum, st.Total)
	}
	t.Logf("计划级统计: %+v", st)
	t.Logf("更正与切换后堆内存 %.1f MB", float64(int64(heapAlloc())-int64(base))/1e6)
}

// TestDeepHistorySingleCase 覆盖单用例深历史：同一用例出现在 10 万个周期中，
// 验证撤回最新候选后回退与切换 revision 的成本不随历史深度增长。
// 运行命令：go test -run TestDeepHistorySingleCase -v
func TestDeepHistorySingleCase(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式下跳过规模测试")
	}
	const nCycles = 100_000
	p := New([]CaseInit{{CaseID: "c", SuiteID: "s", Revision: "r1"}})
	for i := 0; i < nCycles; i++ {
		if err := p.OpenCycle(fmt.Sprintf("cy-%d", i), []string{"c"}); err != nil {
			t.Fatal(err)
		}
	}
	// 全部周期上报 Pass。
	batch := make([]Correction, 1000)
	for done := 0; done < nCycles; done += len(batch) {
		batch = batch[:0]
		for i := 0; i < 1000 && done+i < nCycles; i++ {
			batch = append(batch, Correction{
				CycleID: fmt.Sprintf("cy-%d", done+i),
				CaseID:  "c",
				Status:  Pass,
			})
		}
		if err := p.Apply(batch); err != nil {
			t.Fatal(err)
		}
	}

	// 持续撤回最新候选：每次 O(log E)，10 万次撤回应快速完成。
	start := time.Now()
	for i := nCycles - 1; i >= 0; i-- {
		if err := p.Apply([]Correction{{CycleID: fmt.Sprintf("cy-%d", i), CaseID: "c", Status: NotRun}}); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("深历史: %d 周期, 逐条撤回最新候选耗时 %v", nCycles, time.Since(start))
	st, _ := p.Stats(nil, nil)
	if st.Counts.NotRun != 1 || st.Total != 1 {
		t.Fatalf("全部撤回后应为 NotRun: %+v", st)
	}

	// 深历史下 Activate 应为 O(log E) 级别。
	start = time.Now()
	for i := 0; i < 100_000; i++ {
		rev := "r2"
		if i%2 == 0 {
			rev = "r1"
		}
		if err := p.Activate("c", rev); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("深历史: 100000 次 Activate 耗时 %v", time.Since(start))
}

// TestChurnMemoryStability 在固定数据集上持续撤回重报与切换 revision，
// 验证内存不随操作次数增长。
// 运行命令：go test -run TestChurnMemoryStability -v
func TestChurnMemoryStability(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式下跳过规模测试")
	}
	const (
		nCases  = 1_000
		nCycles = 200
		rounds  = 60 // 每轮对全部位置撤回再重报，并切换全部用例 revision
	)
	inits := make([]CaseInit, nCases)
	for i := range inits {
		inits[i] = CaseInit{CaseID: fmt.Sprintf("c-%d", i), SuiteID: "s", Revision: "r1"}
	}
	p := New(inits)
	positions := make([]Correction, 0, nCases*nCycles)
	for cy := 0; cy < nCycles; cy++ {
		members := make([]string, nCases)
		for i := range members {
			members[i] = fmt.Sprintf("c-%d", i)
		}
		cid := fmt.Sprintf("cy-%d", cy)
		if err := p.OpenCycle(cid, members); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < nCases; i++ {
			positions = append(positions, Correction{CycleID: cid, CaseID: fmt.Sprintf("c-%d", i)})
		}
	}

	report := func(st Status) {
		for off := 0; off < len(positions); off += 1000 {
			end := off + 1000
			if end > len(positions) {
				end = len(positions)
			}
			batch := positions[off:end]
			for i := range batch {
				batch[i].Status = st
			}
			if err := p.Apply(batch); err != nil {
				t.Fatal(err)
			}
		}
	}

	report(Pass)
	before := heapAlloc()
	start := time.Now()
	for r := 0; r < rounds; r++ {
		report(NotRun) // 全部撤回
		report(Pass)   // 全部重报
		rev := "r2"
		if r%2 == 0 {
			rev = "r1"
		}
		for i := 0; i < nCases; i++ {
			if err := p.Activate(fmt.Sprintf("c-%d", i), rev); err != nil {
				t.Fatal(err)
			}
		}
	}
	dur := time.Since(start)
	after := heapAlloc()
	t.Logf("churn: %d 轮 x (%d 撤回 + %d 重报 + %d 切换), 耗时 %v",
		rounds, len(positions), len(positions), nCases, dur)
	t.Logf("churn: 堆内存 前 %.1f MB -> 后 %.1f MB (增长 %.1f MB)",
		float64(before)/1e6, float64(after)/1e6, float64(int64(after)-int64(before))/1e6)

	// 内存上界为 O(C+E)：20 万位置 + 1 千用例，允许少量实现开销，
	// 但 40 万+ 次操作后增长必须远小于操作次数量级。
	const growthLimit = 32 << 20 // 32 MB
	if grown := int64(after) - int64(before); grown > growthLimit {
		t.Fatalf("内存随操作次数增长: 增长 %.1f MB 超过上限 %d MB",
			float64(grown)/1e6, growthLimit>>20)
	}

	st, _ := p.Stats(nil, nil)
	if st.Total != nCases {
		t.Fatalf("计划级分母应为 %d, 得到 %d", nCases, st.Total)
	}
}
