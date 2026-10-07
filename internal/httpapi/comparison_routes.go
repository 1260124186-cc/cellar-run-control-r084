package httpapi

import (
	"net/http"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
)

func registerComparisonRoutes(mux *http.ServeMux, app *service.Service) {
	mux.HandleFunc("GET /v1/formulas/{id}/run-comparison",
		func(response http.ResponseWriter, request *http.Request) {
			comparison, err := app.GetFormulaRunComparison(request.PathValue("id"))
			if err != nil {
				writeDomainError(response, err)
				return
			}
			writeJSON(response, http.StatusOK, comparison)
		})
}
