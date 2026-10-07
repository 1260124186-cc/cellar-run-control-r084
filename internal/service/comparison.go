package service

import (
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// CompareFormulaRuns builds the deterministic completed-run comparison for one
// formula. The comparison is derived entirely inside a single store view, so
// the response is consistent with one snapshot revision.
func (s *Service) CompareFormulaRuns(
	formulaID string,
	field domain.ComparisonSortField,
	direction domain.SortDirection,
) (domain.FormulaRunComparison, error) {
	var comparison domain.FormulaRunComparison
	err := s.store.View(func(snapshot domain.Snapshot) error {
		if _, ok := snapshot.Formulas[formulaID]; !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		runs := make([]domain.FermentationRun, 0)
		for _, run := range snapshot.Runs {
			if run.FormulaID == formulaID {
				runs = append(runs, run)
			}
		}
		comparison = domain.BuildFormulaRunComparison(formulaID, runs, field, direction)
		return nil
	})
	if err != nil {
		return domain.FormulaRunComparison{}, err
	}
	return comparison, nil
}
