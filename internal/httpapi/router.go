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

	"flyway-manager/internal/core"
	"flyway-manager/internal/database"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type RouterOptions struct {
	Database *database.DB
	Service  *core.Service
	Assets   fs.FS
	Logger   *slog.Logger
}

type envelope[T any] struct {
	Data T `json:"data"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
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
	r.Use(serverRequestIDs, securityHeaders, recoverer(logger))
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
			"capabilities":    map[string]bool{"authentication": options.Service != nil, "migration_execution": options.Service != nil && options.Service.ExecutionEnabled(), "registry": options.Service != nil},
		}
		writeJSON(w, status, envelope[map[string]any]{Data: data})
	})
	phase2{s: options.Service, logger: logger, database: options.Database}.register(r)
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

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				if value := recover(); value != nil {
					logger.Error("http handler panic", "request_id", middleware.GetReqID(r.Context()))
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
	if err != nil && errors.Is(err, fs.ErrNotExist) && acceptsHTML(r.Header.Get("Accept")) && (!strings.Contains(path.Base(name), ".") || isDatabaseDetailRoute(name)) && name != "assets" && !strings.HasPrefix(name, "assets/") {
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

// Registry stable IDs can contain dots; deep links still need the SPA shell.
func isDatabaseDetailRoute(name string) bool {
	return strings.HasPrefix(name, "databases/") && strings.Count(name, "/") == 1
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
	w.Header().Set("Cache-Control", "no-store")
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
			logger.Info("http request", "method", r.Method, "path", requestPath(r), "duration", time.Since(started))
		})
	}
}

// Generate correlation IDs here, rather than persisting attacker-controlled header values.
func serverRequestIDs(next http.Handler) http.Handler {
	handler := middleware.RequestID(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Header.Del("X-Request-ID"); handler.ServeHTTP(w, r) })
}

// API route templates exclude caller-supplied path values from diagnostic/security logs.
func requestPath(r *http.Request) string {
	if isAPIPath(r.URL.Path) {
		if route := chi.RouteContext(r.Context()); route != nil {
			if pattern := route.RoutePattern(); pattern != "" {
				return pattern
			}
		}
		return "/api/*"
	}
	return "frontend"
}
