package httpapi

import (
	"flyway-manager/internal/core"
	"flyway-manager/internal/oracle"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (h phase2) registerConnections(r chi.Router) {
	r.Get("/migration-connections", result(h, func(r *http.Request) ([]core.Target, error) {
		return h.s.MigrationConnections(r.Context(), actor(r), page(r))
	}))
	r.Get("/migration-connections/{id}/schemas", result(h, func(r *http.Request) ([]core.Schema, error) {
		return h.s.MigrationSchemas(r.Context(), actor(r), pathID(r), page(r))
	}))
	r.Group(func(r chi.Router) {
		r.Use(h.admin)
		r.Post("/connections/test", bodyResult(h, 200, func(r *http.Request, in core.ConnectionInput) (oracle.Result, error) {
			return h.s.TestConnection(r.Context(), trace(r), "", in)
		}))
		r.Post("/connections/{id}/test", bodyResult(h, 200, func(r *http.Request, in core.ConnectionInput) (oracle.Result, error) {
			return h.s.TestConnection(r.Context(), trace(r), pathID(r), in)
		}))
		r.Post("/connections", bodyResult(h, 201, func(r *http.Request, in core.ConnectionInput) (core.Connection, error) {
			return h.s.SaveConnection(r.Context(), trace(r), "", in)
		}))
		r.Get("/connections/{id}", result(h, func(r *http.Request) (core.Connection, error) {
			return h.s.Connection(r.Context(), actor(r), pathID(r), r.URL.Query().Get("account_id"))
		}))
		r.Put("/connections/{id}", bodyResult(h, 200, func(r *http.Request, in core.ConnectionInput) (core.Connection, error) {
			return h.s.SaveConnection(r.Context(), trace(r), pathID(r), in)
		}))
	})
}
