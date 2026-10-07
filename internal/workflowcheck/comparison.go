package workflowcheck

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// comparisonSnapshot captures a comparison response so the check can verify
// that a restarted process answers the same request byte for byte.
type comparisonSnapshot struct {
	path string
	body []byte
}

type comparisonEntryView struct {
	RunID               string    `json:"run_id"`
	Code                string    `json:"code"`
	PlannedVolumeL      float64   `json:"planned_volume_liters"`
	FinalGravity        float64   `json:"final_gravity"`
	ApparentAttenuation float64   `json:"apparent_attenuation_percent"`
	EstimatedAlcoholABV float64   `json:"estimated_alcohol_abv"`
	ObservationCount    int       `json:"observation_count"`
	CompletedAt         time.Time `json:"completed_at"`
}

type excludedRunView struct {
	RunID  string          `json:"run_id"`
	Code   string          `json:"code"`
	State  domain.RunState `json:"state"`
	Reason string          `json:"reason"`
}

type comparisonSummaryView struct {
	CompletedRuns              int     `json:"completed_runs"`
	TotalPlannedVolumeL        float64 `json:"total_planned_volume_liters"`
	AveragePlannedVolumeL      float64 `json:"average_planned_volume_liters"`
	AverageFinalGravity        float64 `json:"average_final_gravity"`
	AverageApparentAttenuation float64 `json:"average_apparent_attenuation_percent"`
	AverageEstimatedAlcoholABV float64 `json:"average_estimated_alcohol_abv"`
}

type comparisonView struct {
	FormulaID    string                `json:"formula_id"`
	Revision     int64                 `json:"revision"`
	Summary      comparisonSummaryView `json:"summary"`
	Runs         []comparisonEntryView `json:"runs"`
	ExcludedRuns []excludedRunView     `json:"excluded_runs"`
}

func formulaRunComparison(api *client) (comparisonSnapshot, error) {
	formula, err := ensureApprovedFormula(api, "Comparison Formula")
	if err != nil {
		return comparisonSnapshot{}, err
	}
	other, err := ensureApprovedFormula(api, "Comparison Other Formula")
	if err != nil {
		return comparisonSnapshot{}, err
	}
	empty, err := ensureApprovedFormula(api, "Comparison Empty Formula")
	if err != nil {
		return comparisonSnapshot{}, err
	}

	vesselA, err := createCheckVessel(api, "VSL-301", 300)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	vesselB, err := createCheckVessel(api, "VSL-302", 300)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	vesselC, err := createCheckVessel(api, "VSL-303", 300)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	vesselE, err := createCheckVessel(api, "VSL-304", 300)
	if err != nil {
		return comparisonSnapshot{}, err
	}

	// Two completed runs of the same formula with distinct outcomes.
	startedA, err := createStartedRunWithVolume(api, formula.ID, vesselA.ID, "RUN-CMP-A", 180)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	completedA, err := completeCheckedRun(api, startedA, []float64{1.052, 1.030, 1.014})
	if err != nil {
		return comparisonSnapshot{}, err
	}
	startedB, err := createStartedRunWithVolume(api, formula.ID, vesselB.ID, "RUN-CMP-B", 240)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	completedB, err := completeCheckedRun(api, startedB, []float64{1.052, 1.040, 1.028})
	if err != nil {
		return comparisonSnapshot{}, err
	}

	// One fermenting run and one planned run must stay out of the statistics.
	startedC, err := createStartedRunWithVolume(api, formula.ID, vesselC.ID, "RUN-CMP-C", 200)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	if _, err := appendCheckedObservations(api, startedC.Run, []float64{1.052, 1.044}); err != nil {
		return comparisonSnapshot{}, err
	}
	plannedResponse, err := api.call("POST", "/v1/runs",
		createRunPayloadWithVolume("RUN-CMP-D", formula.ID, 150), nil)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireStatus(plannedResponse, 201, "create planned comparison run"); err != nil {
		return comparisonSnapshot{}, err
	}
	var plannedD runView
	if err := plannedResponse.decode(&plannedD); err != nil {
		return comparisonSnapshot{}, err
	}

	// A completed run of another formula must not leak into this comparison.
	startedE, err := createStartedRunWithVolume(api, other.ID, vesselE.ID, "RUN-CMP-E", 180)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	completedE, err := completeCheckedRun(api, startedE, []float64{1.050, 1.020, 1.011})
	if err != nil {
		return comparisonSnapshot{}, err
	}

	path := "/v1/formulas/" + formula.ID + "/run-comparison"
	response, err := api.call("GET", path, nil, nil)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireStatus(response, 200, "read formula run comparison"); err != nil {
		return comparisonSnapshot{}, err
	}
	var view comparisonView
	if err := response.decode(&view); err != nil {
		return comparisonSnapshot{}, err
	}
	if view.FormulaID != formula.ID {
		return comparisonSnapshot{}, errInvalidWorkflow("comparison returned the wrong formula")
	}

	healthResponse, err := api.call("GET", "/healthz", nil, nil)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireStatus(healthResponse, 200, "read health"); err != nil {
		return comparisonSnapshot{}, err
	}
	var health struct {
		Revision int64 `json:"revision"`
	}
	if err := healthResponse.decode(&health); err != nil {
		return comparisonSnapshot{}, err
	}
	if view.Revision != health.Revision {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"comparison revision does not match the snapshot revision")
	}

	// Deterministic order: run A completed before run B.
	if len(view.Runs) != 2 {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"comparison must contain exactly the two completed runs")
	}
	first, second := view.Runs[0], view.Runs[1]
	if first.RunID != completedA.ID || first.Code != "RUN-CMP-A" ||
		second.RunID != completedB.ID || second.Code != "RUN-CMP-B" {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"comparison entries are not in deterministic completion order")
	}
	if second.CompletedAt.Before(first.CompletedAt) {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"comparison entries are not ordered by completion time")
	}
	if err := requireEntry(first, 180, 1.01, 73.08, 4.99, 3); err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireEntry(second, 240, 1.03, 46.15, 3.15, 3); err != nil {
		return comparisonSnapshot{}, err
	}

	// Every aggregated value must trace back to the run detail endpoints.
	if err := requireEntryMatchesRun(api, first); err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireEntryMatchesRun(api, second); err != nil {
		return comparisonSnapshot{}, err
	}

	summary := view.Summary
	if summary.CompletedRuns != 2 ||
		summary.TotalPlannedVolumeL != 420 ||
		summary.AveragePlannedVolumeL != 210 ||
		summary.AverageFinalGravity != 1.02 ||
		summary.AverageEstimatedAlcoholABV != 4.07 {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"comparison summary does not match the contributing runs")
	}
	expectedAttenuation := meanRounded(first.ApparentAttenuation, second.ApparentAttenuation)
	if summary.AverageApparentAttenuation != expectedAttenuation {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"comparison average attenuation is not the rounded mean of its entries")
	}

	// Incomplete runs are listed with reasons instead of mixing into statistics.
	if len(view.ExcludedRuns) != 2 {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"comparison must list both incomplete runs as excluded")
	}
	if err := requireExclusion(view.ExcludedRuns[0], startedC.Run.ID, "RUN-CMP-C",
		domain.RunFermenting, domain.ExclusionRunNotCompleted); err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireExclusion(view.ExcludedRuns[1], plannedD.ID, "RUN-CMP-D",
		domain.RunPlanned, domain.ExclusionRunNotCompleted); err != nil {
		return comparisonSnapshot{}, err
	}
	for _, entry := range view.Runs {
		if entry.RunID == completedE.ID {
			return comparisonSnapshot{}, errInvalidWorkflow(
				"comparison leaked a run from another formula")
		}
	}

	// The other formula sees exactly its own completed run.
	otherResponse, err := api.call("GET", "/v1/formulas/"+other.ID+"/run-comparison", nil, nil)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireStatus(otherResponse, 200, "read other formula comparison"); err != nil {
		return comparisonSnapshot{}, err
	}
	var otherView comparisonView
	if err := otherResponse.decode(&otherView); err != nil {
		return comparisonSnapshot{}, err
	}
	if len(otherView.Runs) != 1 || otherView.Runs[0].RunID != completedE.ID ||
		len(otherView.ExcludedRuns) != 0 || otherView.Summary.CompletedRuns != 1 {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"other formula comparison does not contain exactly its own run")
	}

	// A formula without runs reports a stable empty shape.
	emptyResponse, err := api.call("GET", "/v1/formulas/"+empty.ID+"/run-comparison", nil, nil)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireStatus(emptyResponse, 200, "read empty formula comparison"); err != nil {
		return comparisonSnapshot{}, err
	}
	var emptyView comparisonView
	if err := emptyResponse.decode(&emptyView); err != nil {
		return comparisonSnapshot{}, err
	}
	if len(emptyView.Runs) != 0 || len(emptyView.ExcludedRuns) != 0 ||
		emptyView.Summary.CompletedRuns != 0 || emptyView.Summary.TotalPlannedVolumeL != 0 {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"empty formula comparison must not invent runs or totals")
	}
	if !strings.Contains(string(emptyResponse.body), `"runs":[]`) ||
		!strings.Contains(string(emptyResponse.body), `"excluded_runs":[]`) {
		return comparisonSnapshot{}, errInvalidWorkflow(
			"empty comparison must serialize empty lists, not null")
	}

	missingResponse, err := api.call("GET",
		"/v1/formulas/formula_missing/run-comparison", nil, nil)
	if err != nil {
		return comparisonSnapshot{}, err
	}
	if err := requireCode(missingResponse, 404, string(domain.CodeFormulaNotFound),
		"compare unknown formula"); err != nil {
		return comparisonSnapshot{}, err
	}

	return comparisonSnapshot{path: path, body: response.body}, nil
}

// verifyComparisonAfterRestart confirms that a process restarted on the same
// snapshot answers the comparison request byte for byte identically.
func verifyComparisonAfterRestart(api *client, snapshot comparisonSnapshot) error {
	response, err := api.call("GET", snapshot.path, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(response, 200, "read comparison after restart"); err != nil {
		return err
	}
	if !bytes.Equal(response.body, snapshot.body) {
		return errInvalidWorkflow("comparison response changed after restart")
	}
	return nil
}

func createStartedRunWithVolume(
	api *client,
	formulaID string,
	vesselID string,
	code string,
	volume float64,
) (pairView, error) {
	runResponse, err := api.call("POST", "/v1/runs",
		createRunPayloadWithVolume(code, formulaID, volume), nil)
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(runResponse, 201, "create check run"); err != nil {
		return pairView{}, err
	}
	var run runView
	if err := runResponse.decode(&run); err != nil {
		return pairView{}, err
	}
	reserveResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/reserve",
		map[string]any{"vessel_id": vesselID}, ptrInt64(run.Version))
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(reserveResponse, 200, "reserve check vessel"); err != nil {
		return pairView{}, err
	}
	var reserved pairView
	if err := reserveResponse.decode(&reserved); err != nil {
		return pairView{}, err
	}
	startResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/start",
		nil, ptrInt64(reserved.Run.Version))
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(startResponse, 200, "start check run"); err != nil {
		return pairView{}, err
	}
	var started pairView
	if err := startResponse.decode(&started); err != nil {
		return pairView{}, err
	}
	return started, nil
}

func appendCheckedObservations(
	api *client,
	run runView,
	gravities []float64,
) (runView, error) {
	base := time.Now().UTC().Add(10 * time.Second)
	version := run.Version
	updated := run
	for index, gravity := range gravities {
		sequence := index + 1
		response, err := api.call("POST", "/v1/runs/"+run.ID+"/observations",
			map[string]any{
				"sequence":      sequence,
				"gravity":       gravity,
				"temperature_c": 20.0,
				"observed_at":   verifiedAt(base, sequence),
				"note":          fmt.Sprintf("comparison observation %d", sequence),
			}, ptrInt64(version))
		if err != nil {
			return runView{}, err
		}
		if err := requireStatus(response, 200, "append comparison observation"); err != nil {
			return runView{}, err
		}
		if err := response.decode(&updated); err != nil {
			return runView{}, err
		}
		version = updated.Version
	}
	return updated, nil
}

func completeCheckedRun(
	api *client,
	started pairView,
	gravities []float64,
) (runView, error) {
	observed, err := appendCheckedObservations(api, started.Run, gravities)
	if err != nil {
		return runView{}, err
	}
	conditioningResponse, err := api.call("POST",
		"/v1/runs/"+started.Run.ID+"/conditioning", nil, ptrInt64(observed.Version))
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(conditioningResponse, 200, "condition comparison run"); err != nil {
		return runView{}, err
	}
	var conditioning runView
	if err := conditioningResponse.decode(&conditioning); err != nil {
		return runView{}, err
	}
	completeResponse, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/complete",
		nil, ptrInt64(conditioning.Version))
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(completeResponse, 200, "complete comparison run"); err != nil {
		return runView{}, err
	}
	var completed pairView
	if err := completeResponse.decode(&completed); err != nil {
		return runView{}, err
	}
	if err := requireRunState(completed.Run, domain.RunCompleted); err != nil {
		return runView{}, err
	}
	if completed.Run.Metrics == nil {
		return runView{}, errInvalidWorkflow("completed comparison run is missing metrics")
	}
	return completed.Run, nil
}

func requireEntry(
	entry comparisonEntryView,
	volume float64,
	finalGravity float64,
	attenuation float64,
	alcohol float64,
	observations int,
) error {
	if entry.PlannedVolumeL != volume ||
		entry.FinalGravity != finalGravity ||
		entry.ApparentAttenuation != attenuation ||
		entry.EstimatedAlcoholABV != alcohol ||
		entry.ObservationCount != observations {
		return fmt.Errorf("comparison entry %s does not match the expected metrics",
			entry.Code)
	}
	return nil
}

func requireEntryMatchesRun(api *client, entry comparisonEntryView) error {
	response, err := api.call("GET", "/v1/runs/"+entry.RunID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(response, 200, "read comparison run detail"); err != nil {
		return err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return err
	}
	if run.Metrics == nil || run.EndedAt == nil {
		return errInvalidWorkflow("run detail lost its metrics or completion time")
	}
	if entry.FinalGravity != run.Metrics.FinalGravity ||
		entry.ApparentAttenuation != run.Metrics.ApparentAttenuation ||
		entry.EstimatedAlcoholABV != run.Metrics.EstimatedAlcoholABV ||
		entry.ObservationCount != run.Metrics.ObservationCount ||
		!entry.CompletedAt.Equal(*run.EndedAt) {
		return errInvalidWorkflow("comparison entry does not trace back to the run detail")
	}
	return nil
}

func requireExclusion(
	excluded excludedRunView,
	runID string,
	code string,
	state domain.RunState,
	reason string,
) error {
	if excluded.RunID != runID || excluded.Code != code ||
		excluded.State != state || excluded.Reason != reason {
		return fmt.Errorf("excluded run %s is not reported with state %q and reason %q",
			code, state, reason)
	}
	return nil
}

func meanRounded(values ...float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return math.Round(total/float64(len(values))*100) / 100
}
