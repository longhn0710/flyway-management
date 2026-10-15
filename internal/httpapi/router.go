package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"flyway-manager/internal/database"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type RouterOptions struct {
	Database *database.DB
	Assets   fs.FS
	Logger   *slog.Logger
}

type envelope[T any] struct {
	Data T `json:"data"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorEnvelope struct {
	Error apiError `json:"error"`
}

func NewRouter(options RouterOptions) http.Handler {
	r := chi.NewRouter()
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	r.Use(middleware.RequestID, middleware.RealIP, recoverer(logger))
	r.Use(requestLogger(logger))
	r.Get("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		databaseStatus := "ok"
		status := http.StatusOK
		if options.Database == nil || options.Database.SQL.PingContext(ctx) != nil {
			databaseStatus = "unavailable"
			status = http.StatusServiceUnavailable
		}
		data := map[string]any{
			"status": map[bool]string{true: "ok", false: "degraded"}[databaseStatus == "ok"], "database": databaseStatus,
			"schema_version":  schemaVersion(options.Database),
			"installation_id": installationID(options.Database),
			"capabilities":    map[string]bool{"authentication": false, "migration_execution": false, "registry": false},
		}
		writeJSON(w, status, envelope[map[string]any]{Data: data})
	})
	r.Get("/api/v1/system", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, envelope[map[string]any]{Data: map[string]any{
			"installation_id": installationID(options.Database),
			"schema_version":  schemaVersion(options.Database),
			"capabilities":    map[string]bool{"authentication": false, "migration_execution": false, "registry": false},
		}})
	})
	unimplemented := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "This API capability is not implemented.")
	})
	for _, route := range []string{
		"/api/v1/database-targets", "/api/v1/database-targets/{id}/schemas",
		"/api/v1/artifacts", "/api/v1/artifacts/{id}",
		"/api/v1/migration-runs", "/api/v1/migration-runs/{id}",
		"/api/v1/migration-runs/{id}/logs", "/api/v1/migration-runs/{id}/events",
		"/api/v1/migration-runs/{id}/artifacts", "/api/v1/execution-accounts",
		"/api/v1/access/*", "/api/v1/settings/*",
	} {
		r.Handle(route, unimplemented)
	}
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "API route not found.")
			return
		}
		serveFrontend(w, r, options.Assets)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "HTTP method is not supported for this route.")
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "HTTP method is not supported.")
	})
	return r
}

func isAPIPath(requestPath string) bool {
	return requestPath == "/api" || strings.HasPrefix(requestPath, "/api/")
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				if value := recover(); value != nil {
					logger.Error("http handler panic", "panic", value, "path", r.URL.Path)
					if wrapped.Status() == 0 {
						writeError(wrapped, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
					}
				}
			}()
			next.ServeHTTP(wrapped, r)
		})
	}
}

func serveFrontend(w http.ResponseWriter, r *http.Request, assets fs.FS) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "HTTP method is not supported.")
		return
	}
	if assets == nil {
		writeError(w, http.StatusServiceUnavailable, "ASSETS_UNAVAILABLE", "Frontend assets are unavailable.")
		return
	}
	for _, segment := range strings.Split(r.URL.Path, "/") {
		if segment == ".." {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Asset not found.")
			return
		}
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Asset not found.")
		return
	}
	f, err := assets.Open(name)
	if err != nil && errors.Is(err, fs.ErrNotExist) && acceptsHTML(r.Header.Get("Accept")) && !strings.Contains(path.Base(name), ".") && name != "assets" && !strings.HasPrefix(name, "assets/") {
		name = "index.html"
		f, err = assets.Open(name)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Asset not found.")
		} else {
			writeError(w, http.StatusInternalServerError, "ASSET_READ_ERROR", "Asset could not be opened.")
		}
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Asset not found.")
		} else {
			writeError(w, http.StatusInternalServerError, "ASSET_READ_ERROR", "Asset metadata could not be read.")
		}
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Asset not found.")
		return
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	reader, ok := f.(io.ReadSeeker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "ASSET_READ_ERROR", "Asset could not be served.")
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), reader)
}

func acceptsHTML(header string) bool {
	for _, value := range strings.Split(header, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil || !strings.EqualFold(mediaType, "text/html") {
			continue
		}
		if q, exists := params["q"]; exists {
			quality, err := strconv.ParseFloat(q, 64)
			if err != nil || quality <= 0 {
				continue
			}
		}
		return true
	}
	return false
}

func installationID(db *database.DB) string {
	if db == nil {
		return ""
	}
	return db.InstallationID
}

func schemaVersion(db *database.DB) int {
	if db == nil {
		return 0
	}
	return db.SchemaVersion
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			next.ServeHTTP(w, r)
			logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
		})
	}
}
