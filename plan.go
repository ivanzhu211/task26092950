package qualityplan

import "errors"

// 错误定义。
var (
	ErrUnknownCase    = errors.New("qualityplan: unknown case")
	ErrUnknownCycle   = errors.New("qualityplan: unknown cycle")
	ErrUnknownSuite   = errors.New("qualityplan: unknown suite")
	ErrDuplicateCycle = errors.New("qualityplan: duplicate cycle id")
	ErrEmptyCycle     = errors.New("qualityplan: cycle must contain at least one case")
	ErrCaseNotInCycle = errors.New("qualityplan: case is not a member of the cycle")
	ErrInvalidStatus  = errors.New("qualityplan: invalid status")
	ErrDuplicatePair  = errors.New("qualityplan: duplicate (cycle, case) pair in batch")
)

// revData 记录某一用例在某一冻结 revision 下的全部有效结果：
// live 仅保存非 NotRun 结果（seq -> 状态），heap 按周期序号取最大。
// 内存上界为出现过的不同 (周期, 用例, 修订) 位置数，与操作次数无关。
type revData struct {
	live map[int]Status
	heap *seqHeap
}

func newRevData() *revData {
	return &revData{live: make(map[int]Status), heap: newSeqHeap()}
}

// effective 返回该 revision 下创建序号最大的有效结果。
func (r *revData) effective() (Status, bool) {
	seq, ok := r.heap.top(r.live)
	if !ok {
		return NotRun, false
	}
	return r.live[seq], true
}

// caseState 是单个用例的运行时状态。
type caseState struct {
	suiteID string
	current string
	byRev   map[string]*revData
}

// effective 返回用例当前 revision 下的有效结果。
func (c *caseState) effective() Status {
	rd, ok := c.byRev[c.current]
	if !ok {
		return NotRun
	}
	st, ok := rd.effective()
	if !ok {
		return NotRun
	}
	return st
}

// cycle 是一个执行周期的快照与增量计数。
type cycle struct {
	id           string
	seq          int               // 创建序号，严格递增
	frozen       map[string]string // 成员用例 -> 冻结 revision
	results      map[string]Status // 成员用例 -> 当前结果（仅非 NotRun）
	counts       [statusCount]int  // 全周期计数（含 NotRun 桶）
	suiteCounts  map[string]*[statusCount]int
	suiteMembers map[string]int
}

// Plan 是一个 QualityPlan 的版本感知执行汇总。
// 仅支持顺序调用，非并发安全。
type Plan struct {
	cases       map[string]*caseState
	suites      map[string][]string // 套件 -> 成员用例
	planCounts  [statusCount]int
	suiteCounts map[string]*[statusCount]int
	cycles      []*cycle // 下标即创建序号，严格递增
	cycleByID   map[string]*cycle
}

// New 以固定的用例到套件映射及各用例当前 revision 创建计划。
func New(cases []CaseInit) *Plan {
	p := &Plan{
		cases:       make(map[string]*caseState, len(cases)),
		suites:      make(map[string][]string),
		suiteCounts: make(map[string]*[statusCount]int),
		cycleByID:   make(map[string]*cycle),
	}
	p.planCounts[NotRun] = len(cases)
	for _, ci := range cases {
		if _, ok := p.cases[ci.CaseID]; ok {
			continue // 重复用例ID以首次出现为准
		}
		p.cases[ci.CaseID] = &caseState{
			suiteID: ci.SuiteID,
			current: ci.Revision,
			byRev:   make(map[string]*revData),
		}
		p.suites[ci.SuiteID] = append(p.suites[ci.SuiteID], ci.CaseID)
		sc := p.suiteCounts[ci.SuiteID]
		if sc == nil {
			sc = &[statusCount]int{}
			p.suiteCounts[ci.SuiteID] = sc
		}
		sc[NotRun]++
	}
	return p
}

// bump 对计数器按状态做增量调整。
func bump(counts *[statusCount]int, from, to Status) {
	counts[from]--
	counts[to]++
}

// Activate 将用例的当前修订切换为 revision（可为历史修订）。
// 只影响计划/套件级统计，不改写任何历史周期。
func (p *Plan) Activate(caseID, revision string) error {
	cs, ok := p.cases[caseID]
	if !ok {
		return ErrUnknownCase
	}
	if cs.current == revision {
		return nil
	}
	oldEff := cs.effective()
	cs.current = revision
	newEff := cs.effective()
	if oldEff != newEff {
		bump(&p.planCounts, oldEff, newEff)
		bump(p.suiteCounts[cs.suiteID], oldEff, newEff)
	}
	return nil
}

// OpenCycle 为非空用例集合建立执行周期，冻结每个成员当时的 revision。
// 周期按创建顺序获得严格递增的序号。
func (p *Plan) OpenCycle(cycleID string, caseIDs []string) error {
	if len(caseIDs) == 0 {
		return ErrEmptyCycle
	}
	if _, ok := p.cycleByID[cycleID]; ok {
		return ErrDuplicateCycle
	}
	cy := &cycle{
		id:           cycleID,
		seq:          len(p.cycles),
		frozen:       make(map[string]string, len(caseIDs)),
		results:      make(map[string]Status),
		suiteCounts:  make(map[string]*[statusCount]int),
		suiteMembers: make(map[string]int),
	}
	for _, id := range caseIDs {
		cs, ok := p.cases[id]
		if !ok {
			return ErrUnknownCase
		}
		if _, dup := cy.frozen[id]; dup {
			continue // 输入视为集合，重复成员只计一次
		}
		cy.frozen[id] = cs.current
		cy.counts[NotRun]++
		cy.suiteMembers[cs.suiteID]++
		sc := cy.suiteCounts[cs.suiteID]
		if sc == nil {
			sc = &[statusCount]int{}
			cy.suiteCounts[cs.suiteID] = sc
		}
		sc[NotRun]++
	}
	p.cycles = append(p.cycles, cy)
	p.cycleByID[cycleID] = cy
	return nil
}

// Apply 批量更正周期内的用例结果。批次可跨周期，
// 同一批次内不得重复同一个周期用例对（重复者返回错误）。
func (p *Plan) Apply(batch []Correction) error {
	type pair struct {
		cycle  *cycle
		caseID string
	}
	var seen map[pair]struct{}
	if len(batch) > 16 {
		seen = make(map[pair]struct{}, len(batch))
	}
	for i, c := range batch {
		if c.Status < NotRun || c.Status > Skipped {
			return ErrInvalidStatus
		}
		cy, ok := p.cycleByID[c.CycleID]
		if !ok {
			return ErrUnknownCycle
		}
		frozenRev, member := cy.frozen[c.CaseID]
		if !member {
			if _, known := p.cases[c.CaseID]; !known {
				return ErrUnknownCase
			}
			return ErrCaseNotInCycle
		}
		if seen != nil {
			key := pair{cy, c.CaseID}
			if _, dup := seen[key]; dup {
				return ErrDuplicatePair
			}
			seen[key] = struct{}{}
		} else {
			for _, prev := range batch[:i] {
				if prev.CycleID == c.CycleID && prev.CaseID == c.CaseID {
					return ErrDuplicatePair
				}
			}
		}

		cs := p.cases[c.CaseID]
		old, _ := cy.results[c.CaseID] // 缺省即 NotRun
		if old == c.Status {
			continue
		}

		// 周期级统计只看自身快照，与当前 revision 无关。
		if c.Status == NotRun {
			delete(cy.results, c.CaseID)
		} else {
			cy.results[c.CaseID] = c.Status
		}
		bump(&cy.counts, old, c.Status)
		bump(cy.suiteCounts[cs.suiteID], old, c.Status)

		// 结果始终记录到其冻结 revision 下，切回历史 revision 可重新启用。
		rd := cs.byRev[frozenRev]
		if rd == nil {
			rd = newRevData()
			cs.byRev[frozenRev] = rd
		}
		affectsCurrent := frozenRev == cs.current
		var oldEff Status
		if affectsCurrent {
			oldEff = cs.effective()
		}
		if c.Status == NotRun {
			delete(rd.live, cy.seq)
		} else {
			rd.live[cy.seq] = c.Status
			rd.heap.push(cy.seq)
		}
		if affectsCurrent {
			if newEff := cs.effective(); newEff != oldEff {
				bump(&p.planCounts, oldEff, newEff)
				bump(p.suiteCounts[cs.suiteID], oldEff, newEff)
			}
		}
	}
	return nil
}

// Stats 返回指定范围的统计。cycleID 与 suiteID 传 nil 表示不限定该维度：
//   - (nil, nil)：整个计划
//   - (nil, suite)：套件
//   - (cycle, nil)：周期
//   - (cycle, suite)：周期内套件
func (p *Plan) Stats(cycleID, suiteID *string) (Stats, error) {
	var total int
	var counts [statusCount]int

	if cycleID == nil {
		if suiteID == nil {
			total = len(p.cases)
			counts = p.planCounts
		} else {
			members, ok := p.suites[*suiteID]
			if !ok {
				return Stats{}, ErrUnknownSuite
			}
			total = len(members)
			counts = *p.suiteCounts[*suiteID]
		}
	} else {
		cy, ok := p.cycleByID[*cycleID]
		if !ok {
			return Stats{}, ErrUnknownCycle
		}
		if suiteID == nil {
			total = len(cy.frozen)
			counts = cy.counts
		} else {
			if _, ok := p.suites[*suiteID]; !ok {
				return Stats{}, ErrUnknownSuite
			}
			total = cy.suiteMembers[*suiteID]
			if sc := cy.suiteCounts[*suiteID]; sc != nil {
				counts = *sc
			}
		}
	}
	return makeStats(total, counts), nil
}

// makeStats 由计数与分母计算比例，零分母的比例为零。
func makeStats(total int, counts [statusCount]int) Stats {
	s := Stats{
		Total: total,
		Counts: Counts{
			Pass:    counts[Pass],
			Fail:    counts[Fail],
			Blocked: counts[Blocked],
			Skipped: counts[Skipped],
			NotRun:  counts[NotRun],
		},
	}
	if d := counts[Pass] + counts[Fail]; d > 0 {
		s.PassRate = float64(counts[Pass]) / float64(d)
	}
	if total > 0 {
		s.BlockedRatio = float64(counts[Blocked]) / float64(total)
		s.CompletionRate = float64(counts[Pass]+counts[Fail]+counts[Skipped]) / float64(total)
	}
	return s
}
