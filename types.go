// Package qualityplan 提供 QualityPlan 的版本感知执行汇总能力。
//
// 计划初始化时固定用例到套件的映射及各用例的当前 revision。
// 支持激活修订、开启执行周期、批量更正结果、以及多层级统计。
// 仅支持顺序调用，不做持久化、并发与崩溃恢复。
package qualityplan

// Status 表示单个周期内用例的执行结果。
type Status int

const (
	NotRun Status = iota // 未执行；在 Apply 中表示撤回该位置已有结果
	Pass
	Fail
	Blocked
	Skipped
)

// statusCount 是状态取值的数量。
const statusCount = 5

// CaseInit 描述初始化时一个用例的固定归属与当前修订。
type CaseInit struct {
	CaseID   string
	SuiteID  string
	Revision string
}

// Correction 是 Apply 批次中的一项更正。
type Correction struct {
	CycleID string
	CaseID  string
	Status  Status // NotRun 表示撤回该位置已有结果
}

// Counts 是五种状态的计数，其和恒等于统计范围内的用例数。
type Counts struct {
	Pass    int
	Fail    int
	Blocked int
	Skipped int
	NotRun  int
}

// Stats 是某一统计范围的计数与比例。
type Stats struct {
	Total  int    // 统计范围内的用例数（分母）
	Counts Counts // 五种状态计数，和等于 Total

	PassRate       float64 // Pass / (Pass + Fail)，零分母为 0
	BlockedRatio   float64 // Blocked / Total，零分母为 0
	CompletionRate float64 // (Pass + Fail + Skipped) / Total，零分母为 0
}
