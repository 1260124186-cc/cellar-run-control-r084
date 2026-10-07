package service

import (
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// GetFormulaRunComparison builds the deterministic per-formula comparison of
// completed runs. The result is computed from the persisted snapshot on every
// call, so a restarted process answers identically for unchanged data.
func (s *Service) GetFormulaRunComparison(
	formulaID string,
) (domain.FormulaRunComparison, error) {
	var comparison domain.FormulaRunComparison
	err := s.store.View(func(snapshot domain.Snapshot) error {
		formula, ok := snapshot.Formulas[formulaID]
		if !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		runs := make([]domain.FermentationRun, 0, len(snapshot.Runs))
		for _, run := range snapshot.Runs {
			if run.FormulaID == formula.ID {
				runs = append(runs, run.Clone())
			}
		}
		comparison = domain.BuildFormulaRunComparison(
			formula.ID, runs, snapshot.Revision)
		return nil
	})
	if err != nil {
		return domain.FormulaRunComparison{}, err
	}
	return comparison, nil
}
