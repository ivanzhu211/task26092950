package qualityplan

// Status is the execution result of a case within a cycle.
type Status int

const (
	NotRun Status = iota
	Pass
	Fail
	Blocked
	Skipped
)

const numStatuses = 5

func (s Status) valid() bool {
	return s >= NotRun && s <= Skipped
}

// Correction is a single result update within a batch.
type Correction struct {
	CycleID string
	CaseID  string
	Status  Status
}

// counters holds per-status counts; the five counts always sum to the
// denominator of the scope they describe.
type counters [numStatuses]int64

func (c *counters) move(from, to Status) {
	c[from]--
	c[to]++
}

// Stats is the aggregated result for a plan, suite, cycle, or
// cycle-within-suite scope.
type Stats struct {
	Counts         [numStatuses]int64
	Total          int64
	PassRate       float64
	BlockedRate    float64
	CompletionRate float64
}

func makeStats(counts counters, total int64) Stats {
	st := Stats{Counts: counts, Total: total}
	pass, fail := counts[Pass], counts[Fail]
	if d := pass + fail; d > 0 {
		st.PassRate = float64(pass) / float64(d)
	}
	if total > 0 {
		st.BlockedRate = float64(counts[Blocked]) / float64(total)
		st.CompletionRate = float64(pass+fail+counts[Skipped]) / float64(total)
	}
	return st
}

// StatsQuery selects the scope of a Stats call. Nil fields widen the scope.
type StatsQuery struct {
	CycleID *string
	SuiteID *string
}

// StrPtr is a helper for building StatsQuery values.
func StrPtr(s string) *string { return &s }
