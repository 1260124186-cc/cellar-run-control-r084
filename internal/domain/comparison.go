package domain

import (
	"sort"
	"time"
)

type ComparisonSortField string

const (
	CompareByVolume              ComparisonSortField = "volume"
	CompareByFinalGravity        ComparisonSortField = "final_gravity"
	CompareByApparentAttenuation ComparisonSortField = "apparent_attenuation"
	CompareByAlcoholABV          ComparisonSortField = "alcohol_abv"
)

const DefaultComparisonSortField = CompareByApparentAttenuation

type SortDirection string

const (
	SortAscending  SortDirection = "asc"
	SortDescending SortDirection = "desc"
)

const DefaultSortDirection = SortDescending

func ParseComparisonSortField(raw string) (ComparisonSortField, error) {
	switch field := ComparisonSortField(raw); field {
	case CompareByVolume, CompareByFinalGravity,
		CompareByApparentAttenuation, CompareByAlcoholABV:
		return field, nil
	}
	return "", FieldError(CodeInvalidInput, "sort",
		"must be one of volume, final_gravity, apparent_attenuation, alcohol_abv")
}

func ParseSortDirection(raw string) (SortDirection, error) {
	switch direction := SortDirection(raw); direction {
	case SortAscending, SortDescending:
		return direction, nil
	}
	return "", FieldError(CodeInvalidInput, "direction", "must be asc or desc")
}

type ExclusionReason string

const (
	ExcludedNotCompleted   ExclusionReason = "not_completed"
	ExcludedAborted        ExclusionReason = "aborted"
	ExcludedMissingMetrics ExclusionReason = "missing_metrics"
)

type ExcludedRun struct {
	RunID  string          `json:"run_id"`
	Code   string          `json:"code"`
	State  RunState        `json:"state"`
	Reason ExclusionReason `json:"reason"`
}

type RunComparisonEntry struct {
	RunID               string     `json:"run_id"`
	Code                string     `json:"code"`
	PlannedVolumeL      float64    `json:"planned_volume_liters"`
	FinalGravity        float64    `json:"final_gravity"`
	ApparentAttenuation float64    `json:"apparent_attenuation_percent"`
	EstimatedAlcoholABV float64    `json:"estimated_alcohol_abv"`
	ObservationCount    int        `json:"observation_count"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
}

type MetricStats struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	Avg float64 `json:"avg"`
}

type RunComparisonSummary struct {
	RunCount            int          `json:"run_count"`
	PlannedVolumeL      *MetricStats `json:"planned_volume_liters,omitempty"`
	FinalGravity        *MetricStats `json:"final_gravity,omitempty"`
	ApparentAttenuation *MetricStats `json:"apparent_attenuation_percent,omitempty"`
	EstimatedAlcoholABV *MetricStats `json:"estimated_alcohol_abv,omitempty"`
}

type FormulaRunComparison struct {
	FormulaID    string               `json:"formula_id"`
	Sort         ComparisonSortField  `json:"sort"`
	Direction    SortDirection        `json:"direction"`
	Summary      RunComparisonSummary `json:"summary"`
	Runs         []RunComparisonEntry `json:"runs"`
	ExcludedRuns []ExcludedRun        `json:"excluded_runs"`
}

// BuildFormulaRunComparison compares the completed runs of one formula. Only
// completed runs with calculated metrics contribute to the ranking and the
// summary; every other run is listed explicitly with its exclusion reason so
// incomplete data never leaks silently into the statistics. The result is
// fully deterministic for a given input set.
func BuildFormulaRunComparison(
	formulaID string,
	runs []FermentationRun,
	field ComparisonSortField,
	direction SortDirection,
) FormulaRunComparison {
	comparison := FormulaRunComparison{
		FormulaID:    formulaID,
		Sort:         field,
		Direction:    direction,
		Runs:         []RunComparisonEntry{},
		ExcludedRuns: []ExcludedRun{},
	}
	for _, run := range runs {
		entry, reason, included := comparisonEntry(run)
		if !included {
			comparison.ExcludedRuns = append(comparison.ExcludedRuns, ExcludedRun{
				RunID:  run.ID,
				Code:   run.Code,
				State:  run.State,
				Reason: reason,
			})
			continue
		}
		comparison.Runs = append(comparison.Runs, entry)
	}
	sortComparisonEntries(comparison.Runs, field, direction)
	sortExcludedRuns(comparison.ExcludedRuns)
	comparison.Summary = summarizeComparison(comparison.Runs)
	return comparison
}

func comparisonEntry(run FermentationRun) (RunComparisonEntry, ExclusionReason, bool) {
	if run.State == RunAborted {
		return RunComparisonEntry{}, ExcludedAborted, false
	}
	if run.State != RunCompleted {
		return RunComparisonEntry{}, ExcludedNotCompleted, false
	}
	if run.Metrics == nil {
		return RunComparisonEntry{}, ExcludedMissingMetrics, false
	}
	entry := RunComparisonEntry{
		RunID:               run.ID,
		Code:                run.Code,
		PlannedVolumeL:      run.PlannedVolumeL,
		FinalGravity:        run.Metrics.FinalGravity,
		ApparentAttenuation: run.Metrics.ApparentAttenuation,
		EstimatedAlcoholABV: run.Metrics.EstimatedAlcoholABV,
		ObservationCount:    run.Metrics.ObservationCount,
	}
	if run.EndedAt != nil {
		ended := *run.EndedAt
		entry.CompletedAt = &ended
	}
	return entry, "", true
}

func comparisonSortValue(entry RunComparisonEntry, field ComparisonSortField) float64 {
	switch field {
	case CompareByVolume:
		return entry.PlannedVolumeL
	case CompareByFinalGravity:
		return entry.FinalGravity
	case CompareByAlcoholABV:
		return entry.EstimatedAlcoholABV
	default:
		return entry.ApparentAttenuation
	}
}

func sortComparisonEntries(
	entries []RunComparisonEntry,
	field ComparisonSortField,
	direction SortDirection,
) {
	sort.SliceStable(entries, func(i, j int) bool {
		left := comparisonSortValue(entries[i], field)
		right := comparisonSortValue(entries[j], field)
		if left != right {
			if direction == SortAscending {
				return left < right
			}
			return left > right
		}
		if entries[i].Code != entries[j].Code {
			return entries[i].Code < entries[j].Code
		}
		return entries[i].RunID < entries[j].RunID
	})
}

func sortExcludedRuns(excluded []ExcludedRun) {
	sort.SliceStable(excluded, func(i, j int) bool {
		if excluded[i].Code != excluded[j].Code {
			return excluded[i].Code < excluded[j].Code
		}
		return excluded[i].RunID < excluded[j].RunID
	})
}

func summarizeComparison(entries []RunComparisonEntry) RunComparisonSummary {
	summary := RunComparisonSummary{RunCount: len(entries)}
	if len(entries) == 0 {
		return summary
	}
	summary.PlannedVolumeL = metricStatsFor(entries,
		func(entry RunComparisonEntry) float64 { return entry.PlannedVolumeL })
	summary.FinalGravity = metricStatsFor(entries,
		func(entry RunComparisonEntry) float64 { return entry.FinalGravity })
	summary.ApparentAttenuation = metricStatsFor(entries,
		func(entry RunComparisonEntry) float64 { return entry.ApparentAttenuation })
	summary.EstimatedAlcoholABV = metricStatsFor(entries,
		func(entry RunComparisonEntry) float64 { return entry.EstimatedAlcoholABV })
	return summary
}

// metricStatsFor aggregates the entries in their given (sorted) order so the
// floating-point sum, and therefore the rounded average, is deterministic.
func metricStatsFor(
	entries []RunComparisonEntry,
	pick func(RunComparisonEntry) float64,
) *MetricStats {
	stats := MetricStats{Min: pick(entries[0]), Max: pick(entries[0])}
	sum := 0.0
	for _, entry := range entries {
		value := pick(entry)
		if value < stats.Min {
			stats.Min = value
		}
		if value > stats.Max {
			stats.Max = value
		}
		sum += value
	}
	stats.Avg = round2(sum / float64(len(entries)))
	return &stats
}
