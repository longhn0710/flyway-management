package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strconv"
	"strings"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/core"
	"github.com/go-chi/chi/v5"
)

// registerExternal mounts the project-centric, vendor-neutral API below the
// caller's /api/v2 route. The caller is responsible for ready and authentication
// middleware, as it is for the v1 routes.
func (h phase2) registerExternal(r chi.Router) {
	r.Get("/projects", func(w http.ResponseWriter, r *http.Request) {
		limit, after, err := externalCollectionPage(r, actor(r))
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		items, next, err := h.s.ExternalProjectsPage(r.Context(), actor(r), after, limit)
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		writeExternalCollectionPage(w, r, actor(r), items, limit, next)
	})
	r.Get("/projects/{projectID}", externalResult(h, func(r *http.Request) (any, error) {
		return h.s.ExternalProject(r.Context(), actor(r), chi.URLParam(r, "projectID"))
	}))
	r.Get("/projects/{projectID}/deployment-targets", func(w http.ResponseWriter, r *http.Request) {
		limit, after, err := externalCollectionPage(r, actor(r))
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		items, next, err := h.s.DeploymentTargetsPage(r.Context(), actor(r), chi.URLParam(r, "projectID"), after, limit)
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		writeExternalCollectionPage(w, r, actor(r), items, limit, next)
	})
	r.Get("/projects/{projectID}/deployment-targets/{deploymentTargetID}", externalResult(h, func(r *http.Request) (any, error) {
		return h.s.DeploymentTarget(r.Context(), actor(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "deploymentTargetID"), "read")
	}))
	r.Post("/projects/{projectID}/deployment-targets", externalBody[core.DeploymentTargetInput](h, 201, func(r *http.Request, in core.DeploymentTargetInput) (any, error) {
		return h.s.SaveDeploymentTarget(r.Context(), trace(r), chi.URLParam(r, "projectID"), "", in)
	}))
	r.Patch("/projects/{projectID}/deployment-targets/{deploymentTargetID}", externalBody[core.DeploymentTargetInput](h, 200, func(r *http.Request, in core.DeploymentTargetInput) (any, error) {
		if !actor(r).Admin() {
			return nil, externalError(403, "FORBIDDEN", "Administrator access required.", "")
		}
		if r.Header.Get("If-Match") == "" {
			return nil, externalError(428, "PRECONDITION_REQUIRED", "If-Match with the current Deployment Target revision is required.", "If-Match")
		}
		current, e := h.s.DeploymentTarget(r.Context(), actor(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "deploymentTargetID"), "read")
		if e != nil {
			return nil, e
		}
		if r.Header.Get("If-Match") != fmt.Sprintf("\"%d\"", current.Revision) || in.Revision != current.Revision {
			return nil, externalError(412, "PRECONDITION_FAILED", "Deployment Target revision no longer matches.", "If-Match")
		}
		out, err := h.s.SaveDeploymentTarget(r.Context(), trace(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "deploymentTargetID"), in)
		var ce *core.Error
		if errors.As(err, &ce) && ce.Status == 409 && ce.Message == "Deployment Target revision is stale." {
			return nil, externalError(412, "PRECONDITION_FAILED", "Deployment Target revision no longer matches.", "If-Match")
		}
		return out, err
	}))
	r.Put("/projects/{projectID}/deployment-targets/{deploymentTargetID}/grants", externalBody[core.DeploymentGrantInput](h, 200, func(r *http.Request, in core.DeploymentGrantInput) (any, error) {
		if err := h.s.SetDeploymentGrant(r.Context(), trace(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "deploymentTargetID"), in); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	}))
	r.Get("/projects/{projectID}/deployment-targets/{deploymentTargetID}/grants", externalResult(h, func(r *http.Request) (any, error) {
		return h.s.DeploymentGrants(r.Context(), actor(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "deploymentTargetID"))
	}))
	r.Post("/projects/{projectID}/deployment-targets/{deploymentTargetID}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		p, d := chi.URLParam(r, "projectID"), chi.URLParam(r, "deploymentTargetID")
		if _, err := h.s.DeploymentTarget(r.Context(), actor(r), p, d, "read"); err != nil {
			h.externalFail(w, r, err)
			return
		}
		select {
		case uploadSlots <- struct{}{}:
			defer func() { <-uploadSlots }()
		default:
			w.Header().Set("Retry-After", "5")
			h.externalFail(w, r, externalError(429, "UPLOAD_BUSY", "Upload capacity is busy. Retry later.", ""))
			return
		}
		limits := h.s.ArtifactLimits()
		in, revision, checksum, source, err := readExternalUpload(w, r, limits.MaxUploadBytes)
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		key, err := requiredIdempotencyKey(r)
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		data, err := h.s.UploadExternalArtifact(r.Context(), trace(r), p, d, revision, checksum, key, source, in)
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		writeJSON(w, 201, envelope[any]{Data: data})
	})
	r.Get("/projects/{projectID}/artifacts/{artifactID}", externalResult(h, func(r *http.Request) (any, error) {
		return h.s.ExternalArtifact(r.Context(), actor(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "artifactID"))
	}))
	r.Post("/projects/{projectID}/deployment-targets/{deploymentTargetID}/preflights", externalBody[core.ExternalRunInput](h, 200, func(r *http.Request, in core.ExternalRunInput) (any, error) {
		return h.s.ExternalPreflight(r.Context(), trace(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "deploymentTargetID"), in)
	}))
	r.Get("/projects/{projectID}/preflights/{preflightID}", externalResult(h, func(r *http.Request) (any, error) {
		return h.s.GetExternalPreflight(r.Context(), actor(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "preflightID"))
	}))
	r.Post("/projects/{projectID}/deployment-targets/{deploymentTargetID}/migration-runs", func(w http.ResponseWriter, r *http.Request) {
		var in core.ExternalRunInput
		if err := readExternalJSON(w, r, &in); err != nil {
			h.externalFail(w, r, err)
			return
		}
		key, err := requiredIdempotencyKey(r)
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		p := chi.URLParam(r, "projectID")
		data, err := h.s.SubmitExternalRun(r.Context(), trace(r), p, chi.URLParam(r, "deploymentTargetID"), in, key)
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		id := externalID(data)
		if id != "" {
			w.Header().Set("Location", "/api/v2/projects/"+p+"/migration-runs/"+id)
		}
		w.Header().Set("Retry-After", "2")
		writeJSON(w, 202, envelope[any]{Data: data})
	})
	r.Get("/projects/{projectID}/migration-runs", func(w http.ResponseWriter, r *http.Request) {
		p := chi.URLParam(r, "projectID")
		limit, after, err := externalCollectionPage(r, actor(r))
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		items, next, err := h.s.ExternalRunsPage(r.Context(), actor(r), p, after, limit)
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		writeExternalCollectionPage(w, r, actor(r), items, limit, next)
	})
	r.Get("/projects/{projectID}/migration-runs/{runID}", externalResult(h, func(r *http.Request) (any, error) {
		return h.s.ExternalRun(r.Context(), actor(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "runID"))
	}))
	r.Get("/projects/{projectID}/migration-runs/{runID}/logs", func(w http.ResponseWriter, r *http.Request) {
		limit, offset, err := externalPage(r, actor(r))
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		items, err := h.s.ExternalLogs(r.Context(), actor(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "runID"), core.Page{Limit: limit, Offset: offset})
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		page := map[string]any{"limit": limit}
		if len(items) == limit {
			page["next_cursor"] = encodeExternalCursor(r.URL.Path, actor(r), offset+len(items))
		}
		writeJSON(w, 200, map[string]any{"data": items, "page": page})
	})
	r.Get("/projects/{projectID}/migration-runs/{runID}/record", func(w http.ResponseWriter, r *http.Request) {
		data, err := h.s.ExternalRecord(r.Context(), actor(r), chi.URLParam(r, "projectID"), chi.URLParam(r, "runID"))
		if err != nil {
			h.externalFail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		digest := sha256.Sum256(data)
		w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base("run-" + chi.URLParam(r, "runID") + ".json")}))
		w.WriteHeader(200)
		_, _ = w.Write(data)
	})
}

type problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance,omitempty"`
	Code     string `json:"code,omitempty"`
	Field    string `json:"field,omitempty"`
}

func externalError(status int, code, msg, field string) error {
	return &core.Error{Status: status, Code: code, Message: msg, Field: field}
}
func (h phase2) externalFail(w http.ResponseWriter, r *http.Request, err error) {
	var ce *core.Error
	if !errors.As(err, &ce) {
		ce = &core.Error{Status: 500, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred."}
		h.logger.Error("external API operation failed", "request_id", trace(r).RequestID)
	}
	if h.s != nil && (ce.Status == 401 || ce.Status == 403 || ce.Status == 409 || (mutating(r.Method) && ce.Status >= 400 && ce.Status < 500)) {
		if auditErr := h.s.Event(r.Context(), trace(r), "request.denied", requestPath(r), "failure"); auditErr != nil {
			ce = &core.Error{Status: 500, Code: "INTERNAL_ERROR", Message: "Security event could not be recorded."}
		}
	}
	title := http.StatusText(ce.Status)
	if title == "" {
		title = "Request failed"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(ce.Status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: title, Status: ce.Status, Detail: ce.Message, Instance: r.URL.Path, Code: ce.Code, Field: ce.Field})
}
func externalResult(h phase2, fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, e := fn(r)
		if e != nil {
			h.externalFail(w, r, e)
			return
		}
		if d, ok := v.(core.DeploymentTarget); ok {
			w.Header().Set("ETag", fmt.Sprintf("\"%d\"", d.Revision))
		}
		writeJSON(w, 200, envelope[any]{Data: v})
	}
}
func externalBody[I any](h phase2, status int, fn func(*http.Request, I) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in I
		if e := readExternalJSON(w, r, &in); e != nil {
			h.externalFail(w, r, e)
			return
		}
		v, e := fn(r, in)
		if e != nil {
			h.externalFail(w, r, e)
			return
		}
		writeJSON(w, status, envelope[any]{Data: v})
	}
}
func readExternalJSON(w http.ResponseWriter, r *http.Request, out any) error {
	if err := readBody(w, r, out); err != nil {
		var ce *core.Error
		if errors.As(err, &ce) {
			return ce
		}
		return externalError(400, "INVALID_REQUEST", "Invalid JSON request.", "")
	}
	return nil
}
func requiredIdempotencyKey(r *http.Request) (string, error) {
	k := r.Header.Get("Idempotency-Key")
	if strings.TrimSpace(k) == "" || len(k) > 200 {
		return "", externalError(400, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key must contain 1 to 200 characters.", "Idempotency-Key")
	}
	return k, nil
}
func externalID(v any) string {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "run_id"} {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}

type cursorState struct {
	Scope, Actor string
	Offset       int
	After        string
}

var externalCursorKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("cannot initialize external API cursor signing key")
	}
	return key
}()

func encodeExternalCursor(scope string, a core.Actor, o int) string {
	b, _ := json.Marshal(cursorState{Scope: scope, Actor: a.Kind + ":" + a.ID, Offset: o})
	return signExternalCursor(b)
}
func encodeExternalAfterCursor(scope string, a core.Actor, after string) string {
	b, _ := json.Marshal(cursorState{Scope: scope, Actor: a.Kind + ":" + a.ID, After: after})
	return signExternalCursor(b)
}
func signExternalCursor(b []byte) string {
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, externalCursorKey)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func externalCursor(r *http.Request, a core.Actor) (cursorState, error) {
	var state cursorState
	c := r.URL.Query().Get("cursor")
	if c == "" {
		return state, nil
	}
	parts := strings.Split(c, ".")
	if len(c) > 512 || len(parts) != 2 {
		return state, externalError(400, "INVALID_CURSOR", "The cursor is invalid for this caller or resource.", "cursor")
	}
	provided, sigErr := base64.RawURLEncoding.DecodeString(parts[1])
	mac := hmac.New(sha256.New, externalCursorKey)
	_, _ = mac.Write([]byte(parts[0]))
	if sigErr != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		return state, externalError(400, "INVALID_CURSOR", "The cursor is invalid for this caller or resource.", "cursor")
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(b, &state) != nil || state.Scope != r.URL.Path || state.Actor != a.Kind+":"+a.ID {
		return cursorState{}, externalError(400, "INVALID_CURSOR", "The cursor is invalid for this caller or resource.", "cursor")
	}
	return state, nil
}
func externalPage(r *http.Request, a core.Actor) (int, int, error) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 200 {
			return 0, 0, externalError(400, "INVALID_PAGE", "limit must be between 1 and 200.", "limit")
		}
		limit = n
	}
	s, e := externalCursor(r, a)
	if e != nil {
		return 0, 0, e
	}
	if s.After != "" || s.Offset < 0 || s.Offset > 1_000_000_000 {
		return 0, 0, externalError(400, "INVALID_CURSOR", "The cursor is invalid for this caller or resource.", "cursor")
	}
	return limit, s.Offset, nil
}

func externalCollectionPage(r *http.Request, a core.Actor) (int, string, error) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 200 {
			return 0, "", externalError(400, "INVALID_PAGE", "limit must be between 1 and 200.", "limit")
		}
		limit = n
	}
	s, e := externalCursor(r, a)
	if e != nil {
		return 0, "", e
	}
	if s.Offset != 0 {
		return 0, "", externalError(400, "INVALID_CURSOR", "The cursor is invalid for this caller or resource.", "cursor")
	}
	return limit, s.After, nil
}

func writeExternalCollectionPage[T any](w http.ResponseWriter, r *http.Request, a core.Actor, items []T, limit int, next string) {
	page := map[string]any{"limit": limit}
	if next != "" {
		page["next_cursor"] = encodeExternalAfterCursor(r.URL.Path, a, next)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "page": page})
}

func readExternalUpload(w http.ResponseWriter, r *http.Request, maxUpload int64) (artifact.Input, int, string, core.ExternalSource, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(64<<10))
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "multipart/form-data" {
		return artifact.Input{}, 0, "", core.ExternalSource{}, externalError(415, "UNSUPPORTED_MEDIA_TYPE", "Use multipart/form-data.", "")
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	fields := map[string]string{}
	var file artifact.Input
	fileCount := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return file, 0, "", core.ExternalSource{}, externalError(413, "REQUEST_TOO_LARGE", "Upload exceeds configured request limits.", "")
			}
			return file, 0, "", core.ExternalSource{}, externalError(400, "INVALID_MULTIPART", "Malformed multipart body.", "")
		}
		name := part.FormName()
		if name != "files" && name != "deployment_target_revision" && name != "input_sha256" && name != "external_source" {
			part.Close()
			return file, 0, "", core.ExternalSource{}, externalError(400, "INVALID_MULTIPART", "Unknown multipart field.", name)
		}
		if name == "files" {
			fileCount++
			if fileCount > 1 {
				part.Close()
				return file, 0, "", core.ExternalSource{}, externalError(400, "ONE_ARCHIVE_REQUIRED", "Upload exactly one ZIP file.", "files")
			}
			data, err := io.ReadAll(io.LimitReader(part, maxUpload+1))
			part.Close()
			if err != nil || int64(len(data)) > maxUpload {
				return file, 0, "", core.ExternalSource{}, externalError(413, "REQUEST_TOO_LARGE", "Upload exceeds 50 MiB.", "files")
			}
			file = artifact.Input{Name: part.FileName(), Bytes: data}
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, 8193))
		part.Close()
		if err != nil || len(data) > 8192 {
			return file, 0, "", core.ExternalSource{}, externalError(413, "REQUEST_TOO_LARGE", "Multipart field is too large.", name)
		}
		if _, exists := fields[name]; exists {
			return file, 0, "", core.ExternalSource{}, externalError(400, "INVALID_MULTIPART", "Duplicate multipart field.", name)
		}
		fields[name] = string(data)
	}
	if fileCount != 1 {
		return file, 0, "", core.ExternalSource{}, externalError(400, "ONE_ARCHIVE_REQUIRED", "Upload exactly one ZIP file in field files.", "files")
	}
	if len(file.Bytes) < 4 || !bytes.HasPrefix(file.Bytes, []byte("PK\x03\x04")) {
		return file, 0, "", core.ExternalSource{}, externalError(415, "ZIP_REQUIRED", "The uploaded artifact must be a ZIP archive.", "files")
	}
	checksum := strings.ToLower(strings.TrimSpace(fields["input_sha256"]))
	digest := sha256.Sum256(file.Bytes)
	actual := hex.EncodeToString(digest[:])
	if len(checksum) != 64 || checksum != actual {
		return file, 0, "", core.ExternalSource{}, externalError(422, "CHECKSUM_MISMATCH", "input_sha256 must match the uploaded ZIP bytes.", "input_sha256")
	}
	var source core.ExternalSource
	if raw := fields["external_source"]; raw != "" {
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&source); err != nil {
			return file, 0, "", source, externalError(400, "INVALID_EXTERNAL_SOURCE", "external_source must be a valid JSON object.", "external_source")
		}
		if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
			return file, 0, "", source, externalError(400, "INVALID_EXTERNAL_SOURCE", "external_source must contain one JSON object.", "external_source")
		}
		if len(raw) > 4096 {
			return file, 0, "", source, externalError(413, "REQUEST_TOO_LARGE", "external_source exceeds 4 KiB.", "external_source")
		}
	}
	revision, err := strconv.Atoi(fields["deployment_target_revision"])
	if err != nil || revision < 1 {
		return file, 0, "", source, externalError(400, "INVALID_REQUEST", "deployment_target_revision must be a positive integer.", "deployment_target_revision")
	}
	return file, revision, actual, source, nil
}
