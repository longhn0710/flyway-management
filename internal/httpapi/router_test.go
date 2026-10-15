package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flyway-manager/internal/database"
)

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	assetsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetsDir, "index.html"), []byte("app shell"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(assetsDir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "assets", "app.js"), []byte("bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	assets, err := fs.Sub(os.DirFS(assetsDir), ".")
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(RouterOptions{Database: db, Assets: assets})
}

func request(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	handler.ServeHTTP(rec, req)
	return rec
}

func TestHealthAndSystemMetadata(t *testing.T) {
	handler := testRouter(t)
	for _, target := range []string{"/api/v1/health", "/api/v1/system"} {
		rec := request(handler, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body: %s", target, rec.Code, rec.Body.String())
		}
		var result struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data["installation_id"] == "" {
			t.Fatalf("GET %s has no installation ID", target)
		}
		if result.Data["schema_version"] != float64(1) {
			t.Fatalf("GET %s schema version = %v", target, result.Data["schema_version"])
		}
	}
	rec := request(handler, http.MethodGet, "/api/v1/health")
	var result struct {
		Data struct {
			Status, Database string
			Capabilities     map[string]bool
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Status != "ok" || result.Data.Database != "ok" {
		t.Fatalf("health = %+v", result.Data)
	}
	if result.Data.Capabilities["authentication"] || result.Data.Capabilities["migration_execution"] || result.Data.Capabilities["registry"] {
		t.Fatalf("unexpected enabled capabilities: %+v", result.Data.Capabilities)
	}
}

func TestUnsupportedAPIRoutesReturnTyped501AndUnknownAPIReturnsJSON404(t *testing.T) {
	handler := testRouter(t)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		rec := request(handler, method, "/api/v1/database-targets")
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s unsupported route status = %d; body %s", method, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"NOT_IMPLEMENTED"`) {
			t.Fatalf("missing typed error: %s", rec.Body.String())
		}
	}
	for _, target := range []string{"/api", "/api/", "/api/v1/missing", "/api/unknown"} {
		rec := request(handler, http.MethodGet, target)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("unknown API response for %s = %d %q", target, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	if rec := request(handler, http.MethodPost, "/api/v1/health"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST health status = %d", rec.Code)
	}
}

func TestStaticAssetsSPAAndMissingAssets(t *testing.T) {
	handler := testRouter(t)
	for target, want := range map[string]string{"/": "app shell", "/assets/app.js": "bundle", "/runs/123": "app shell"} {
		rec := request(handler, http.MethodGet, target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("GET %s = %d %q", target, rec.Code, rec.Body.String())
		}
	}
	for _, target := range []string{"/assets/missing.js", "/missing.png"} {
		rec := request(handler, http.MethodGet, target)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("GET %s = %d %q", target, rec.Code, rec.Body.String())
		}
	}
	noAccept := httptest.NewRecorder()
	handler.ServeHTTP(noAccept, httptest.NewRequest(http.MethodGet, "/missing-route", nil))
	if noAccept.Code != http.StatusNotFound || !strings.Contains(noAccept.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("missing extensionless asset without HTML Accept = %d %q", noAccept.Code, noAccept.Body.String())
	}
	noHTML := httptest.NewRecorder()
	requestNoAccept := httptest.NewRequest(http.MethodGet, "/missing-route", nil)
	requestNoAccept.Header.Set("Accept", "text/html;q=0,application/json")
	handler.ServeHTTP(noHTML, requestNoAccept)
	if noHTML.Code != http.StatusNotFound {
		t.Fatalf("SPA fallback ignored zero HTML quality: %d", noHTML.Code)
	}
	for _, target := range []string{"/../secret", "/%2e%2e/secret"} {
		rec := request(handler, http.MethodGet, target)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("traversal path %s = %d %q", target, rec.Code, rec.Body.String())
		}
	}
	if rec := request(handler, http.MethodPost, "/runs/123"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST SPA route status = %d", rec.Code)
	}
}

type statFailureFS struct{ err error }

func (f statFailureFS) Open(string) (fs.File, error) { return statFailureFile{err: f.err}, nil }

type statFailureFile struct{ err error }

func (statFailureFile) Read([]byte) (int, error)     { return 0, io.EOF }
func (statFailureFile) Close() error                 { return nil }
func (f statFailureFile) Stat() (fs.FileInfo, error) { return nil, f.err }

func TestAssetStatFailureReturns500JSON(t *testing.T) {
	db, err := database.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewRouter(RouterOptions{Database: db, Assets: statFailureFS{err: errors.New("stat failed")}})
	rec := request(handler, http.MethodGet, "/app.js")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("stat failure response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestRecovererUsesTypedJSONEnvelope(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := recoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("panic response = %d %q", rec.Code, rec.Body.String())
	}
	var result errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error.Code != "INTERNAL_ERROR" || result.Error.Message == "" || strings.Contains(result.Error.Message, "test panic") {
		t.Fatalf("panic error envelope = %+v", result.Error)
	}
}
