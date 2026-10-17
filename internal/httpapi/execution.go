package httpapi

import (
	"errors"
	"flyway-manager/internal/artifact"
	"flyway-manager/internal/core"
	"flyway-manager/internal/flyway"
	"github.com/go-chi/chi/v5"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
)

var uploadSlots = make(chan struct{}, 2)

func (h phase2) registerExecution(r chi.Router) {
	r.Get("/runtime", result(h, func(r *http.Request) (flyway.Readiness, error) { return h.s.Runtime(r.Context()), nil }))
	r.Get("/database-targets/{id}/schemas/{schemaID}/execution-accounts", result(h, func(r *http.Request) ([]core.ExecutionAccount, error) {
		return h.s.StreamAccounts(r.Context(), actor(r), pathID(r), chi.URLParam(r, "schemaID"))
	}))
	r.Post("/artifacts", h.uploadArtifact)
	r.Get("/artifacts/{id}", result(h, func(r *http.Request) (core.Artifact, error) { return h.s.Artifact(r.Context(), actor(r), pathID(r)) }))
	for _, resource := range []string{"files", "inputs"} {
		r.Get("/artifacts/{id}/"+resource, func(w http.ResponseWriter, r *http.Request) {
			original := resource == "inputs"
			index := 0
			if original {
				var e error
				index, e = strconv.Atoi(r.URL.Query().Get("index"))
				if e != nil || index < 0 {
					h.fail(w, r, &core.Error{Status: 400, Code: "VALIDATION_ERROR", Message: "A nonnegative input index is required.", Field: "index"})
					return
				}
			}
			name, content, e := h.s.ArtifactBytes(r.Context(), actor(r), pathID(r), r.URL.Query().Get("path"), index, original)
			if e != nil {
				h.fail(w, r, e)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			_, _ = w.Write(content)
		})
	}
	r.Post("/migration-preflight", bodyResult(h, 200, func(r *http.Request, in core.RunInput) (core.Preflight, error) {
		return h.s.Preflight(r.Context(), trace(r), in)
	}))
	r.Post("/migration-runs", bodyResult(h, 202, func(r *http.Request, in core.RunInput) (core.Run, error) {
		return h.s.SubmitRun(r.Context(), trace(r), in, r.Header.Get("Idempotency-Key"))
	}))
	r.Get("/migration-runs", result(h, func(r *http.Request) ([]core.Run, error) {
		q := r.URL.Query()
		return h.s.Runs(r.Context(), actor(r), core.RunFilter{Page: page(r), Status: q.Get("status"), TargetID: q.Get("target_id"), SchemaID: q.Get("schema_id"), Actor: q.Get("actor"), Source: q.Get("source"), From: q.Get("from"), To: q.Get("to")})
	}))
	r.Get("/migration-runs/{id}", result(h, func(r *http.Request) (core.Run, error) { return h.s.Run(r.Context(), actor(r), pathID(r)) }))
	r.Get("/migration-runs/{id}/logs", result(h, func(r *http.Request) ([]core.RunLog, error) {
		return h.s.RunLogs(r.Context(), actor(r), pathID(r), page(r), r.URL.Query().Get("channel"))
	}))
	r.Get("/migration-runs/{id}/artifacts", result(h, func(r *http.Request) ([]core.Artifact, error) {
		return h.s.RunArtifacts(r.Context(), actor(r), pathID(r))
	}))
	r.Get("/migration-runs/{id}/files", result(h, func(r *http.Request) ([]core.RunFile, error) {
		return h.s.RunFiles(r.Context(), actor(r), pathID(r))
	}))
	r.Get("/migration-runs/{id}/files/content", func(w http.ResponseWriter, r *http.Request) {
		name, content, e := h.s.RunFileBytes(r.Context(), actor(r), pathID(r), r.URL.Query().Get("artifact_id"), r.URL.Query().Get("path"))
		if e != nil {
			h.fail(w, r, e)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = w.Write(content)
	})
	r.Get("/migration-runs/{id}/executions", result(h, func(r *http.Request) ([]core.RunExecution, error) {
		return h.s.RunExecutions(r.Context(), actor(r), pathID(r))
	}))
	r.Get("/migration-runs/{id}/repair", result(h, func(r *http.Request) (core.Repair, error) { return h.s.RunRepair(r.Context(), actor(r), pathID(r)) }))
	r.Post("/migration-runs/{id}/cancel", empty(h, func(r *http.Request) error { return h.s.CancelRun(r.Context(), trace(r), pathID(r)) }))
	r.Get("/migration-runs/{id}/record", func(w http.ResponseWriter, r *http.Request) {
		rid := pathID(r)
		run, e := h.s.Run(r.Context(), actor(r), rid)
		if e != nil {
			h.fail(w, r, e)
			return
		}
		// Runs have a fixed log budget, so this download has a bounded total size.
		logs := []core.RunLog{}
		for offset := 0; ; offset += 200 {
			batch, e := h.s.RunLogs(r.Context(), actor(r), rid, core.Page{Limit: 200, Offset: offset}, "")
			if e != nil {
				h.fail(w, r, e)
				return
			}
			logs = append(logs, batch...)
			if len(batch) < 200 {
				break
			}
		}
		executions, e := h.s.RunExecutions(r.Context(), actor(r), rid)
		if e != nil {
			h.fail(w, r, e)
			return
		}
		repair, e := h.s.RunRepair(r.Context(), actor(r), rid)
		if e != nil {
			h.fail(w, r, e)
			return
		}
		artifacts, e := h.s.RunArtifacts(r.Context(), actor(r), rid)
		if e != nil {
			h.fail(w, r, e)
			return
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "run-" + rid + ".json"}))
		writeJSON(w, 200, envelope[map[string]any]{Data: map[string]any{"run": run, "logs": logs, "executions": executions, "repair": repair, "artifacts": artifacts}})
	})
}
func (h phase2) uploadArtifact(w http.ResponseWriter, r *http.Request) {
	target, schema := r.URL.Query().Get("target_id"), r.URL.Query().Get("schema_id")
	if schema == "" {
		h.fail(w, r, &core.Error{Status: 400, Code: "VALIDATION_ERROR", Message: "A target schema is required.", Field: "schema_id"})
		return
	}
	if e := h.s.Authorize(r.Context(), actor(r), "operate", target, schema); e != nil {
		h.fail(w, r, e)
		return
	}
	select {
	case uploadSlots <- struct{}{}:
		defer func() { <-uploadSlots }()
	default:
		writeError(w, 429, "UPLOAD_BUSY", "Upload capacity is busy. Retry later.")
		return
	}
	limits := h.s.ArtifactLimits()
	r.Body = http.MaxBytesReader(w, r.Body, limits.MaxUploadBytes+(64<<10))
	mr, e := r.MultipartReader()
	if e != nil {
		h.fail(w, r, &core.Error{Status: 415, Code: "UNSUPPORTED_MEDIA_TYPE", Message: "Use multipart/form-data with files parts."})
		return
	}
	var inputs []artifact.Input
	var total int64
	for {
		part, e := mr.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			h.uploadError(w, r, e)
			return
		}
		_, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if e != nil || params["name"] != "files" || params["filename"] == "" {
			part.Close()
			h.fail(w, r, &core.Error{Status: 400, Code: "VALIDATION_ERROR", Message: "Every multipart part must be a named files upload."})
			return
		}
		if len(inputs) >= limits.MaxFiles {
			part.Close()
			h.uploadError(w, r, artifact.ErrLimit)
			return
		}
		content, e := io.ReadAll(io.LimitReader(part, limits.MaxUploadBytes-total+1))
		part.Close()
		if e != nil {
			h.uploadError(w, r, e)
			return
		}
		total += int64(len(content))
		if total > limits.MaxUploadBytes {
			h.uploadError(w, r, artifact.ErrLimit)
			return
		}
		// multipart.Part.FileName strips directories; preserve and validate the raw
		// filename parameter so folder uploads retain their immutable relative paths.
		inputs = append(inputs, artifact.Input{Name: params["filename"], Bytes: content})
	}
	out, e := h.s.UploadArtifact(r.Context(), trace(r), target, schema, inputs)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	writeJSON(w, 201, envelope[core.Artifact]{Data: out})
}
func (h phase2) uploadError(w http.ResponseWriter, r *http.Request, e error) {
	var tooLarge *http.MaxBytesError
	if errors.Is(e, artifact.ErrLimit) || errors.As(e, &tooLarge) {
		h.fail(w, r, &core.Error{Status: 413, Code: "ARTIFACT_LIMIT", Message: "Upload exceeds configured limits."})
		return
	}
	h.fail(w, r, &core.Error{Status: 400, Code: "INVALID_UPLOAD", Message: "Malformed or incomplete multipart upload."})
}
