package workflowcheck

import (
	"bytes"
	"fmt"
	"math"
	"time"
)

type metricStatsView struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	Avg float64 `json:"avg"`
}

type comparisonEntryView struct {
	RunID               string  `json:"run_id"`
	Code                string  `json:"code"`
	PlannedVolumeL      float64 `json:"planned_volume_liters"`
	FinalGravity        float64 `json:"final_gravity"`
	ApparentAttenuation float64 `json:"apparent_attenuation_percent"`
	EstimatedAlcoholABV float64 `json:"estimated_alcohol_abv"`
	ObservationCount    int     `json:"observation_count"`
}

type excludedRunView struct {
	RunID  string `json:"run_id"`
	Code   string `json:"code"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type comparisonView struct {
	FormulaID string `json:"formula_id"`
	Sort      string `json:"sort"`
	Direction string `json:"direction"`
	Summary   struct {
		RunCount            int              `json:"run_count"`
		PlannedVolumeL      *metricStatsView `json:"planned_volume_liters"`
		FinalGravity        *metricStatsView `json:"final_gravity"`
		ApparentAttenuation *metricStatsView `json:"apparent_attenuation_percent"`
		EstimatedAlcoholABV *metricStatsView `json:"estimated_alcohol_abv"`
	} `json:"summary"`
	Runs         []comparisonEntryView `json:"runs"`
	ExcludedRuns []excludedRunView     `json:"excluded_runs"`
}

func formulaRunComparison(dataDir string) error {
	server, err := startCheckServer(dataDir)
	if err != nil {
		return err
	}
	api := newClient(server.url)

	formula, err := ensureApprovedFormula(api, "Comparison Formula")
	if err != nil {
		return err
	}

	runA, err := completeCheckRun(api, formula.ID, "VSL-301", "RUN-COMPARE-A", 180,
		[]float64{1.050, 1.028, 1.014})
	if err != nil {
		return err
	}
	runB, err := completeCheckRun(api, formula.ID, "VSL-302", "RUN-COMPARE-B", 200,
		[]float64{1.050, 1.030, 1.018})
	if err != nil {
		return err
	}

	vesselC, err := createCheckVessel(api, "VSL-303", 300)
	if err != nil {
		return err
	}
	if _, err := createStartedRun(api, formula.ID, vesselC.ID, "RUN-COMPARE-OPEN"); err != nil {
		return err
	}

	abortedResponse, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-COMPARE-HALTED", formula.ID), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(abortedResponse, 201, "create abort candidate"); err != nil {
		return err
	}
	var abortCandidate runView
	if err := abortedResponse.decode(&abortCandidate); err != nil {
		return err
	}
	abortResponse, err := api.call("POST", "/v1/runs/"+abortCandidate.ID+"/abort",
		nil, ptrInt64(abortCandidate.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(abortResponse, 200, "abort planned run"); err != nil {
		return err
	}

	defaultResponse, err := api.call("GET",
		"/v1/formulas/"+formula.ID+"/run-comparison", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(defaultResponse, 200, "read run comparison"); err != nil {
		return err
	}
	var comparison comparisonView
	if err := defaultResponse.decode(&comparison); err != nil {
		return err
	}
	if comparison.Sort != "apparent_attenuation" || comparison.Direction != "desc" {
		return errInvalidWorkflow("comparison did not report the default sort")
	}
	if err := requireRunOrder(comparison, "RUN-COMPARE-A", "RUN-COMPARE-B"); err != nil {
		return err
	}
	if err := requireExcludedRuns(comparison,
		excludedRunView{Code: "RUN-COMPARE-HALTED", State: "aborted", Reason: "aborted"},
		excludedRunView{Code: "RUN-COMPARE-OPEN", State: "fermenting", Reason: "not_completed"},
	); err != nil {
		return err
	}
	if err := requireSummaryStats(comparison.Runs,
		func(e comparisonEntryView) float64 { return e.PlannedVolumeL },
		comparison.Summary.PlannedVolumeL, "planned_volume_liters"); err != nil {
		return err
	}
	if err := requireSummaryStats(comparison.Runs,
		func(e comparisonEntryView) float64 { return e.FinalGravity },
		comparison.Summary.FinalGravity, "final_gravity"); err != nil {
		return err
	}
	if err := requireSummaryStats(comparison.Runs,
		func(e comparisonEntryView) float64 { return e.ApparentAttenuation },
		comparison.Summary.ApparentAttenuation, "apparent_attenuation_percent"); err != nil {
		return err
	}
	if err := requireSummaryStats(comparison.Runs,
		func(e comparisonEntryView) float64 { return e.EstimatedAlcoholABV },
		comparison.Summary.EstimatedAlcoholABV, "estimated_alcohol_abv"); err != nil {
		return err
	}
	if err := requireEntryMetrics(api, comparison.Runs[0], runA,
		1.01, 73.08, 4.99, 180); err != nil {
		return err
	}
	if err := requireEntryMetrics(api, comparison.Runs[1], runB,
		1.02, 65.38, 4.46, 200); err != nil {
		return err
	}

	orderCases := []struct {
		query string
		codes []string
	}{
		{"sort=final_gravity&direction=asc", []string{"RUN-COMPARE-A", "RUN-COMPARE-B"}},
		{"sort=final_gravity&direction=desc", []string{"RUN-COMPARE-B", "RUN-COMPARE-A"}},
		{"sort=volume&direction=asc", []string{"RUN-COMPARE-A", "RUN-COMPARE-B"}},
		{"sort=volume&direction=desc", []string{"RUN-COMPARE-B", "RUN-COMPARE-A"}},
		{"sort=alcohol_abv&direction=asc", []string{"RUN-COMPARE-B", "RUN-COMPARE-A"}},
		{"sort=apparent_attenuation&direction=asc", []string{"RUN-COMPARE-B", "RUN-COMPARE-A"}},
	}
	for _, orderCase := range orderCases {
		sorted, err := fetchComparison(api, formula.ID, orderCase.query)
		if err != nil {
			return err
		}
		if err := requireRunOrder(sorted, orderCase.codes...); err != nil {
			return fmt.Errorf("%s: %w", orderCase.query, err)
		}
	}

	invalidSort, err := api.call("GET",
		"/v1/formulas/"+formula.ID+"/run-comparison?sort=style", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(invalidSort, 400, "invalid_input", "reject unknown sort field"); err != nil {
		return err
	}
	invalidDirection, err := api.call("GET",
		"/v1/formulas/"+formula.ID+"/run-comparison?direction=sideways", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(invalidDirection, 400, "invalid_input",
		"reject unknown sort direction"); err != nil {
		return err
	}

	missingFormula, err := api.call("GET",
		"/v1/formulas/formula_missing/run-comparison", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(missingFormula, 404, "formula_not_found",
		"compare runs of a missing formula"); err != nil {
		return err
	}

	emptyFormula, err := ensureApprovedFormula(api, "Empty Comparison Formula")
	if err != nil {
		return err
	}
	empty, err := fetchComparison(api, emptyFormula.ID, "")
	if err != nil {
		return err
	}
	if empty.Summary.RunCount != 0 || len(empty.Runs) != 0 || len(empty.ExcludedRuns) != 0 ||
		empty.Summary.ApparentAttenuation != nil || empty.Summary.FinalGravity != nil ||
		empty.Summary.PlannedVolumeL != nil || empty.Summary.EstimatedAlcoholABV != nil {
		return errInvalidWorkflow("formula without completed runs produced statistics")
	}

	sortedResponse, err := api.call("GET",
		"/v1/formulas/"+formula.ID+"/run-comparison?sort=final_gravity&direction=asc",
		nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(sortedResponse, 200, "read sorted comparison"); err != nil {
		return err
	}

	server.close()
	restarted, err := startCheckServer(dataDir)
	if err != nil {
		return err
	}
	defer restarted.close()
	restartedAPI := newClient(restarted.url)

	defaultAfterRestart, err := restartedAPI.call("GET",
		"/v1/formulas/"+formula.ID+"/run-comparison", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(defaultAfterRestart, 200, "read comparison after restart"); err != nil {
		return err
	}
	if !bytes.Equal(defaultAfterRestart.body, defaultResponse.body) {
		return errInvalidWorkflow("comparison changed after a restart")
	}
	sortedAfterRestart, err := restartedAPI.call("GET",
		"/v1/formulas/"+formula.ID+"/run-comparison?sort=final_gravity&direction=asc",
		nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(sortedAfterRestart, 200, "read sorted comparison after restart"); err != nil {
		return err
	}
	if !bytes.Equal(sortedAfterRestart.body, sortedResponse.body) {
		return errInvalidWorkflow("sorted comparison changed after a restart")
	}
	return nil
}

func completeCheckRun(
	api *client,
	formulaID string,
	vesselCode string,
	runCode string,
	volume float64,
	gravities []float64,
) (runView, error) {
	vessel, err := createCheckVessel(api, vesselCode, 300)
	if err != nil {
		return runView{}, err
	}
	started, err := startRunWithPayload(api, map[string]any{
		"code":                  runCode,
		"formula_id":            formulaID,
		"planned_volume_liters": volume,
		"notes":                 "comparison run",
	}, vessel.ID)
	if err != nil {
		return runView{}, err
	}
	base := time.Now().UTC().Add(10 * time.Second)
	version := started.Run.Version
	for index, gravity := range gravities {
		sequence := index + 1
		response, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/observations",
			map[string]any{
				"sequence":      sequence,
				"gravity":       gravity,
				"temperature_c": 20.0,
				"observed_at":   verifiedAt(base, sequence),
				"note":          fmt.Sprintf("observation %d", sequence),
			}, ptrInt64(version))
		if err != nil {
			return runView{}, err
		}
		if err := requireStatus(response, 200, "append comparison observation"); err != nil {
			return runView{}, err
		}
		var run runView
		if err := response.decode(&run); err != nil {
			return runView{}, err
		}
		version = run.Version
	}
	conditioningResponse, err := api.call("POST",
		"/v1/runs/"+started.Run.ID+"/conditioning", nil, ptrInt64(version))
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
	return completed.Run, nil
}

func fetchComparison(api *client, formulaID, query string) (comparisonView, error) {
	path := "/v1/formulas/" + formulaID + "/run-comparison"
	if query != "" {
		path += "?" + query
	}
	response, err := api.call("GET", path, nil, nil)
	if err != nil {
		return comparisonView{}, err
	}
	if err := requireStatus(response, 200, "read run comparison"); err != nil {
		return comparisonView{}, err
	}
	var comparison comparisonView
	if err := response.decode(&comparison); err != nil {
		return comparisonView{}, err
	}
	return comparison, nil
}

func requireRunOrder(comparison comparisonView, codes ...string) error {
	if len(comparison.Runs) != len(codes) {
		return fmt.Errorf("expected %d compared runs, got %d",
			len(codes), len(comparison.Runs))
	}
	for index, code := range codes {
		if comparison.Runs[index].Code != code {
			return fmt.Errorf("expected compared run %d to be %q, got %q",
				index, code, comparison.Runs[index].Code)
		}
	}
	return nil
}

func requireExcludedRuns(comparison comparisonView, expected ...excludedRunView) error {
	if len(comparison.ExcludedRuns) != len(expected) {
		return fmt.Errorf("expected %d excluded runs, got %d",
			len(expected), len(comparison.ExcludedRuns))
	}
	for index, wanted := range expected {
		actual := comparison.ExcludedRuns[index]
		if actual.Code != wanted.Code || actual.State != wanted.State ||
			actual.Reason != wanted.Reason || actual.RunID == "" {
			return fmt.Errorf("excluded run %d: got %+v, want code %q state %q reason %q",
				index, actual, wanted.Code, wanted.State, wanted.Reason)
		}
	}
	return nil
}

func requireSummaryStats(
	entries []comparisonEntryView,
	pick func(comparisonEntryView) float64,
	stats *metricStatsView,
	label string,
) error {
	if stats == nil {
		return fmt.Errorf("comparison summary is missing %s statistics", label)
	}
	min := pick(entries[0])
	max := min
	sum := 0.0
	for _, entry := range entries {
		value := pick(entry)
		if value < min {
			min = value
		}
		if value > max {
			max = value
		}
		sum += value
	}
	average := math.Round(sum/float64(len(entries))*100) / 100
	if stats.Min != min || stats.Max != max || stats.Avg != average {
		return fmt.Errorf(
			"comparison %s statistics do not match run details: got min %v max %v avg %v, want min %v max %v avg %v",
			label, stats.Min, stats.Max, stats.Avg, min, max, average)
	}
	return nil
}

func requireEntryMetrics(
	api *client,
	entry comparisonEntryView,
	source runView,
	finalGravity float64,
	attenuation float64,
	alcohol float64,
	volume float64,
) error {
	if entry.RunID != source.ID || entry.Code == "" {
		return errInvalidWorkflow("comparison entry does not identify its run")
	}
	if entry.FinalGravity != finalGravity ||
		entry.ApparentAttenuation != attenuation ||
		entry.EstimatedAlcoholABV != alcohol ||
		entry.PlannedVolumeL != volume ||
		entry.ObservationCount != 3 {
		return fmt.Errorf("comparison entry for %q has unexpected metrics: %+v",
			entry.Code, entry)
	}
	detailResponse, err := api.call("GET", "/v1/runs/"+entry.RunID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(detailResponse, 200, "trace comparison entry to run"); err != nil {
		return err
	}
	var detail runView
	if err := detailResponse.decode(&detail); err != nil {
		return err
	}
	if detail.Metrics == nil ||
		detail.Metrics.FinalGravity != entry.FinalGravity ||
		detail.Metrics.ApparentAttenuation != entry.ApparentAttenuation ||
		detail.Metrics.EstimatedAlcoholABV != entry.EstimatedAlcoholABV ||
		detail.Metrics.ObservationCount != entry.ObservationCount ||
		len(detail.Observations) != entry.ObservationCount {
		return fmt.Errorf("comparison entry for %q does not match its run detail", entry.Code)
	}
	return nil
}
