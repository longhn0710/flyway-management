package httpapi

import (
	"flyway-manager/internal/core"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (h phase2) registerFlywayInfo(r chi.Router) {
	r.Get("/database-targets/{id}/schemas/{schemaID}/flyway-info", result(h, func(r *http.Request) (*core.InfoCapture, error) {
		return h.s.LatestInfo(r.Context(), actor(r), pathID(r), chi.URLParam(r, "schemaID"), r.URL.Query().Get("run_id"))
	}))
	r.Post("/database-targets/{id}/schemas/{schemaID}/flyway-info/refresh", bodyResult(h, 200, func(r *http.Request, in core.RunInput) (core.InfoCapture, error) {
		in.TargetID = pathID(r)
		in.SchemaID = chi.URLParam(r, "schemaID")
		return h.s.RefreshInfo(r.Context(), trace(r), in)
	}))
}
