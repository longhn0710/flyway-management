package httpapi

import (
	"flyway-manager/internal/core"
	"github.com/go-chi/chi/v5"
	"net/http"
)

// registerProjects must be called inside the authenticated /api/v1 group.
func (h phase2) registerProjects(r chi.Router) {
	r.Get("/projects", result(h, func(r *http.Request) ([]core.Project, error) {
		return h.s.Projects(r.Context(), actor(r), r.URL.Query().Get("q"))
	}))
	r.Get("/projects/{id}", result(h, func(r *http.Request) (core.Project, error) { return h.s.Project(r.Context(), actor(r), pathID(r)) }))
	r.Get("/projects/{id}/migration-runs", result(h, func(r *http.Request) ([]core.Run, error) {
		f := core.RunFilter{Page: page(r), Status: r.URL.Query().Get("status"), TargetID: r.URL.Query().Get("target_id"), SchemaID: r.URL.Query().Get("schema_id"), Actor: r.URL.Query().Get("actor"), Source: r.URL.Query().Get("source"), From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to")}
		return h.s.ProjectRuns(r.Context(), actor(r), pathID(r), f)
	}))
	r.Group(func(r chi.Router) {
		r.Use(h.admin)
		r.Delete("/projects/{id}", bodyResult(h, 200, func(r *http.Request, in core.ProjectDeleteInput) (map[string]bool, error) {
			err := h.s.DeleteProject(r.Context(), trace(r), pathID(r), in)
			return map[string]bool{"deleted": true}, err
		}))
		r.Post("/projects", bodyResult(h, 201, func(r *http.Request, in core.ProjectInput) (core.Project, error) {
			return h.s.SaveProject(r.Context(), trace(r), "", in)
		}))
		r.Put("/projects/{id}", bodyResult(h, 200, func(r *http.Request, in core.ProjectInput) (core.Project, error) {
			return h.s.SaveProject(r.Context(), trace(r), pathID(r), in)
		}))
	})
}
