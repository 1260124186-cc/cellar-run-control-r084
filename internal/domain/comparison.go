package domain

import (
	"sort"
	"time"
)

// Exclusion reasons used by FormulaRunComparison. A run that does not take
// part in the comparison statistics is always listed under ExcludedRuns with
// one of these reasons, so incomplete data never mixes into the aggregates
// silently.
const (
	ExclusionRunNotCompleted   = "run_not_completed"
	ExclusionIncompleteMetrics = "incomplete_metrics"
)

// RunComparisonEntry is one completed run inside a formula comparison. The
// metric values are copied from the run's stored RunMetrics so the entry
// always matches the run detail returned by the run read endpoints.
type RunComparisonEntry struct {
	RunID               string    `json:"run_id"`
	Code                string    `json:"code"`
	PlannedVolumeL      float64   `json:"planned_volume_liters"`
	FinalGravity        float64   `json:"final_gravity"`
	ApparentAttenuation float64   `json:"apparent_attenuation_percent"`
	EstimatedAlcoholABV float64   `json:"estimated_alcohol_abv"`
	ObservationCount    int       `json:"observation_count"`
	CompletedAt         time.Time `json:"completed_at"`
}

// ExcludedRun identifies a run of the formula that was kept out of the
// comparison statistics, together with the reason.
type ExcludedRun struct {
	RunID  string   `json:"run_id"`
	Code   string   `json:"code"`
	State  RunState `json:"state"`
	Reason string   `json:"reason"`
}

// RunComparisonSummary aggregates the comparison entries. Averages are
// computed over the sorted entries with the same rounding rule as run
// metrics, so the summary is deterministic for a given snapshot.
type RunComparisonSummary struct {
	CompletedRuns              int     `json:"completed_runs"`
	TotalPlannedVolumeL        float64 `json:"total_planned_volume_liters"`
	AveragePlannedVolumeL      float64 `json:"average_planned_volume_liters"`
	AverageFinalGravity        float64 `json:"average_final_gravity"`
	AverageApparentAttenuation float64 `json:"average_apparent_attenuation_percent"`
	AverageEstimatedAlcoholABV float64 `json:"average_estimated_alcohol_abv"`
}

// FormulaRunComparison compares the completed runs of one formula. Revision
// is the snapshot revision the comparison was computed from; Runs and
// ExcludedRuns carry run identifiers so every aggregate stays traceable to
// the run detail endpoints.
type FormulaRunComparison struct {
	FormulaID    string               `json:"formula_id"`
	Revision     int64                `json:"revision"`
	Summary      RunComparisonSummary `json:"summary"`
	Runs         []RunComparisonEntry `json:"runs"`
	ExcludedRuns []ExcludedRun        `json:"excluded_runs"`
}

// BuildFormulaRunComparison aggregates the runs of one formula into a
// deterministic comparison. Only completed runs whose stored metrics are
// consistent with their observations are aggregated; every other run is
// listed under ExcludedRuns with a reason. The runs slice is not mutated.
func BuildFormulaRunComparison(
	formulaID string,
	runs []FermentationRun,
	revision int64,
) FormulaRunComparison {
	entries := make([]RunComparisonEntry, 0, len(runs))
	excluded := make([]ExcludedRun, 0, len(runs))
	for _, run := range runs {
		if run.State != RunCompleted {
			excluded = append(excluded, ExcludedRun{
				RunID:  run.ID,
				Code:   run.Code,
				State:  run.State,
				Reason: ExclusionRunNotCompleted,
			})
			continue
		}
		if !comparisonMetricsComplete(run) {
			excluded = append(excluded, ExcludedRun{
				RunID:  run.ID,
				Code:   run.Code,
				State:  run.State,
				Reason: ExclusionIncompleteMetrics,
			})
			continue
		}
		entries = append(entries, RunComparisonEntry{
			RunID:               run.ID,
			Code:                run.Code,
			PlannedVolumeL:      run.PlannedVolumeL,
			FinalGravity:        run.Metrics.FinalGravity,
			ApparentAttenuation: run.Metrics.ApparentAttenuation,
			EstimatedAlcoholABV: run.Metrics.EstimatedAlcoholABV,
			ObservationCount:    run.Metrics.ObservationCount,
			CompletedAt:         *run.EndedAt,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].CompletedAt.Equal(entries[j].CompletedAt) {
			return entries[i].CompletedAt.Before(entries[j].CompletedAt)
		}
		if entries[i].Code != entries[j].Code {
			return entries[i].Code < entries[j].Code
		}
		return entries[i].RunID < entries[j].RunID
	})
	sort.Slice(excluded, func(i, j int) bool {
		if excluded[i].Code != excluded[j].Code {
			return excluded[i].Code < excluded[j].Code
		}
		return excluded[i].RunID < excluded[j].RunID
	})
	return FormulaRunComparison{
		FormulaID:    formulaID,
		Revision:     revision,
		Summary:      summarizeComparison(entries),
		Runs:         entries,
		ExcludedRuns: excluded,
	}
}

// comparisonMetricsComplete reports whether a completed run carries metrics
// that are consistent with its stored observations.
func comparisonMetricsComplete(run FermentationRun) bool {
	if run.Metrics == nil || run.EndedAt == nil {
		return false
	}
	if len(run.Observations) < 3 {
		return false
	}
	return run.Metrics.ObservationCount == len(run.Observations)
}

func summarizeComparison(entries []RunComparisonEntry) RunComparisonSummary {
	summary := RunComparisonSummary{CompletedRuns: len(entries)}
	if len(entries) == 0 {
		return summary
	}
	var volume, gravity, attenuation, alcohol float64
	for _, entry := range entries {
		volume += entry.PlannedVolumeL
		gravity += entry.FinalGravity
		attenuation += entry.ApparentAttenuation
		alcohol += entry.EstimatedAlcoholABV
	}
	count := float64(len(entries))
	summary.TotalPlannedVolumeL = round2(volume)
	summary.AveragePlannedVolumeL = round2(volume / count)
	summary.AverageFinalGravity = round2(gravity / count)
	summary.AverageApparentAttenuation = round2(attenuation / count)
	summary.AverageEstimatedAlcoholABV = round2(alcohol / count)
	return summary
}
