package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"flyway-manager/internal/core"
	"flyway-manager/internal/database"
)

type apiFixture struct {
	handler http.Handler
	db      *database.DB
	service *core.Service
	cookie  *http.Cookie
	csrf    string
	admin   core.User
	logs    *bytes.Buffer
}

func apiSetup(t *testing.T) *apiFixture {
	t.Helper()
	dir := t.TempDir()
	db, e := database.Open(context.Background(), dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	service, e := core.New(context.Background(), db, dir, "")
	if e != nil {
		t.Fatal(e)
	}
	logs := new(bytes.Buffer)
	f := &apiFixture{db: db, service: service, logs: logs}
	f.handler = NewRouter(RouterOptions{Database: db, Service: service, Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	token, e := os.ReadFile(service.SetupTokenFile)
	if e != nil {
		t.Fatal(e)
	}
	rec := f.call("POST", "/auth/bootstrap", map[string]any{"username": "admin", "password": "test-password-for-admin", "setup_token": strings.TrimSpace(string(token))}, nil, nil)
	if rec.Code != 201 {
		t.Fatalf("bootstrap: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "fm_session" {
			f.cookie = c
		}
	}
	var out envelope[core.SessionResult]
	if e = json.Unmarshal(rec.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	f.csrf = out.Data.CSRFToken
	f.admin = out.Data.User
	return f
}
func (f *apiFixture) call(method, path string, body any, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		if text, ok := body.(string); ok {
			reader = strings.NewReader(text)
		} else {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
	}
	req := httptest.NewRequest(method, "http://manager.test/api/v1"+path, reader)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Origin", "http://manager.test")
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", f.csrf)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}
func decodeData[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d want %d: %s", rec.Code, status, rec.Body.String())
	}
	var out envelope[T]
	if e := json.Unmarshal(rec.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out.Data
}
func assertStatus(t *testing.T, r *httptest.ResponseRecorder, want int) {
	t.Helper()
	if r.Code != want {
		t.Fatalf("status %d want %d: %s", r.Code, want, r.Body.String())
	}
}
func TestAPIEndToEndIdentityRegistryCredentialsAndServiceToken(t *testing.T) {
	f := apiSetup(t)
	if f.cookie == nil || !f.cookie.HttpOnly || f.cookie.SameSite != http.SameSiteLaxMode || f.cookie.Secure {
		t.Fatal("invalid loopback session cookie")
	}
	me := decodeData[core.SessionResult](t, f.call("GET", "/auth/me", nil, f.cookie, nil), 200)
	if me.User.ID != f.admin.ID || me.CSRFToken != f.csrf {
		t.Fatal("identity changed on reload")
	}
	target := decodeData[core.Target](t, f.call("POST", "/database-targets", map[string]any{"id": "uat", "name": "Oracle UAT", "environment": "UAT", "host": "db.example", "port": 1521, "service": "APP", "transport": "TCP"}, f.cookie, nil), 201)
	schema := decodeData[core.Schema](t, f.call("POST", "/database-targets/uat/schemas", map[string]string{"name": "CORE_BANKING"}, f.cookie, nil), 201)
	schema2 := decodeData[core.Schema](t, f.call("POST", "/database-targets/uat/schemas", map[string]string{"name": "PAYMENT"}, f.cookie, nil), 201)
	account := decodeData[core.ExecutionAccount](t, f.call("POST", "/execution-accounts", map[string]any{"target_id": target.ID, "name": "Deployer", "username": "DEPLOY_ADMIN", "schema_ids": []string{schema.ID, schema2.ID}, "password": "oracle-do-not-leak"}, f.cookie, nil), 201)
	if !account.Credential.Configured || account.Credential.Version != 1 {
		t.Fatal("credential missing")
	}
	decodeData[core.Schema](t, f.call("PUT", "/database-targets/uat/schemas/"+schema.ID, map[string]any{"name": schema.Name, "default_account_id": account.ID, "active": true}, f.cookie, nil), 200)
	decodeData[core.CredentialMetadata](t, f.call("PUT", "/execution-accounts/"+account.ID+"/credential", map[string]string{"password": "new-oracle-password"}, f.cookie, nil), 200)
	listing := f.call("GET", "/execution-accounts", nil, f.cookie, nil)
	assertStatus(t, listing, 200)
	if strings.Contains(listing.Body.String(), "password") || strings.Contains(listing.Body.String(), "payload") {
		t.Fatal("credential leaked via metadata")
	}
	sa := decodeData[core.ServiceAccount](t, f.call("POST", "/access/service-accounts", map[string]string{"name": "pipeline", "purpose": "migrate", "owner_id": f.admin.ID}, f.cookie, nil), 201)
	decodeData[[]core.Grant](t, f.call("PUT", "/access/service-accounts/"+sa.ID+"/grants", map[string]any{"grants": []core.Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true, Operate: true}}}, f.cookie, nil), 200)
	issued := decodeData[core.IssuedToken](t, f.call("POST", "/access/service-accounts/"+sa.ID+"/tokens", map[string]string{"name": "build", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, f.cookie, nil), 201)
	bearer := map[string]string{"Authorization": "Bearer " + issued.Token}
	scoped := decodeData[[]core.Schema](t, f.call("GET", "/database-targets/uat/schemas", nil, nil, bearer), 200)
	if len(scoped) != 1 || scoped[0].ID != schema.ID {
		t.Fatal("bearer schema scope escaped")
	}
	assertStatus(t, f.call("GET", "/database-targets/uat/schemas/"+schema2.ID, nil, nil, bearer), 403)
	assertStatus(t, f.call("GET", "/access/users", nil, nil, bearer), 403)
	assertStatus(t, f.call("GET", "/auth/me", nil, nil, bearer), 403)
	assertStatus(t, f.call("POST", "/migration-runs", map[string]string{}, nil, bearer), 400)
	tokens := f.call("GET", "/access/service-accounts/"+sa.ID+"/tokens", nil, f.cookie, nil)
	assertStatus(t, tokens, 200)
	if strings.Contains(tokens.Body.String(), issued.Token) {
		t.Fatal("token listing leaks secret")
	}
	assertStatus(t, f.call("DELETE", "/access/service-accounts/"+sa.ID+"/tokens/"+issued.Metadata.ID, nil, f.cookie, nil), 200)
	assertStatus(t, f.call("GET", "/database-targets", nil, nil, bearer), 401)
	events := decodeData[[]core.AuditEvent](t, f.call("GET", "/access/audit?limit=200", nil, f.cookie, nil), 200)
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Action] = true
		if e.RequestID == "" {
			t.Fatal("missing audit correlation")
		}
		if strings.HasPrefix(e.Action, "credential.") && e.ActorID != f.admin.ID {
			t.Fatal("untrusted actor attribution")
		}
	}
	for _, action := range []string{"auth.bootstrap", "target.create", "schema.create", "credential.create", "credential.replace", "permissions.update", "token.issue", "auth.token", "token.revoke"} {
		if !seen[action] {
			t.Fatalf("missing audit %s", action)
		}
	}
	logText := f.logs.String()
	for _, secret := range []string{"test-password-for-admin", "oracle-do-not-leak", "new-oracle-password", issued.Token, f.cookie.Value, f.csrf} {
		if strings.Contains(logText, secret) {
			t.Fatal("secret leaked into logs")
		}
	}
	assertStatus(t, f.call("POST", "/auth/logout", map[string]any{}, f.cookie, nil), 200)
	assertStatus(t, f.call("GET", "/auth/me", nil, f.cookie, nil), 401)
}
func TestAPICSFRRequestValidationAndNoPreviewBypass(t *testing.T) {
	f := apiSetup(t)
	for _, headers := range []map[string]string{{"Origin": "https://evil.example"}, {"Origin": "null"}, {"Origin": ""}, {"X-CSRF-Token": ""}, {"Sec-Fetch-Site": "cross-site"}} {
		r := f.call("POST", "/database-targets", map[string]any{}, f.cookie, headers)
		assertStatus(t, r, 403)
		if !strings.Contains(r.Body.String(), "CSRF_REJECTED") {
			t.Fatal("missing typed csrf error")
		}
	}
	assertStatus(t, f.call("POST", "/auth/login", map[string]string{}, nil, map[string]string{"Origin": "https://evil.example"}), 403)
	assertStatus(t, f.call("POST", "/database-targets", map[string]any{}, nil, map[string]string{"X-Preview": "true", "X-Authenticated-User": "admin", "X-Role": "Admin"}), 401)
	assertStatus(t, f.call("POST", "/database-targets", map[string]any{}, f.cookie, map[string]string{"Authorization": "Bearer forged"}), 400)
	assertStatus(t, f.call("POST", "/database-targets", `{"id":"x","actor":"admin"}`, f.cookie, nil), 400)
	assertStatus(t, f.call("POST", "/database-targets", `{} {}`, f.cookie, nil), 400)
	assertStatus(t, f.call("POST", "/database-targets", map[string]string{}, f.cookie, map[string]string{"Content-Type": "text/plain"}), 415)
	assertStatus(t, f.call("POST", "/database-targets", `{"name":"`+strings.Repeat("a", 1<<20)+`"}`, f.cookie, nil), 413)
	for _, path := range []string{"/auth/me", "/auth/sessions", "/access/users", "/access/audit", "/access/service-accounts", "/database-targets", "/database-targets/a/schemas", "/execution-accounts", "/settings/authentication", "/migration-runs", "/artifacts/unknown"} {
		assertStatus(t, f.call("GET", path, nil, nil, nil), 401)
	}
	// A direct TLS request sets Secure without trusting forwarded headers.
	req := httptest.NewRequest("POST", "https://manager.test/api/v1/auth/login", strings.NewReader(`{"provider":"local","username":"admin","password":"test-password-for-admin"}`))
	req.Header.Set("Origin", "https://manager.test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	assertStatus(t, rec, 200)
	if len(rec.Result().Cookies()) == 0 || !rec.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS cookie not secure")
	}
}
func TestAPIRolesDisabledSessionsAndGenericLoginErrors(t *testing.T) {
	f := apiSetup(t)
	for _, role := range []string{"Operator", "Viewer"} {
		user := decodeData[core.User](t, f.call("POST", "/access/users", map[string]string{"username": strings.ToLower(role), "password": "test-password-for-user", "role": role}, f.cookie, nil), 201)
		login := f.call("POST", "/auth/login", map[string]string{"provider": "local", "username": user.Username, "password": "test-password-for-user"}, nil, nil)
		identity := decodeData[core.SessionResult](t, login, 200)
		cookie := login.Result().Cookies()[0]
		for _, path := range []string{"/access/users", "/access/service-accounts", "/execution-accounts", "/settings/authentication", "/access/audit"} {
			assertStatus(t, f.call("GET", path, nil, cookie, nil), 403)
		}
		assertStatus(t, f.call("POST", "/database-targets", map[string]any{}, cookie, map[string]string{"X-CSRF-Token": identity.CSRFToken}), 403)
		decodeData[core.User](t, f.call("PUT", "/access/users/"+user.ID, map[string]any{"display_name": user.Username, "role": role, "active": false}, f.cookie, nil), 200)
		assertStatus(t, f.call("GET", "/auth/me", nil, cookie, nil), 401)
	}
	unknown := f.call("POST", "/auth/login", map[string]string{"provider": "local", "username": "unknown", "password": "invalid"}, nil, nil)
	disabled := f.call("POST", "/auth/login", map[string]string{"provider": "local", "username": "viewer", "password": "test-password-for-user"}, nil, nil)
	assertStatus(t, unknown, 401)
	assertStatus(t, disabled, 401)
	if unknown.Body.String() != disabled.Body.String() {
		t.Fatal("login enumeration")
	}
}
func TestAPIAuthRateLimitAndHTTPSProxyOrigin(t *testing.T) {
	f := apiSetup(t)
	for i := 0; i < 9; i++ {
		assertStatus(t, f.call("POST", "/auth/login", map[string]string{"provider": "local", "username": "admin", "password": "invalid"}, nil, nil), 401)
	}
	rec := f.call("POST", "/auth/login", map[string]string{}, nil, nil)
	assertStatus(t, rec, 429)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing retry hint")
	}
	// Configured origin works behind a TLS terminator; arbitrary forwarding headers do not.
	f.service.PublicURL = "https://console.example"
	assertStatus(t, f.call("POST", "/auth/logout", map[string]any{}, f.cookie, map[string]string{"Origin": "https://console.example"}), 200)
}

func TestOIDCCallbackRejectsForgedBrowserStateAndExpiredCookie(t *testing.T) {
	f := apiSetup(t)
	r := f.call("GET", "/auth/oidc/callback?state=attacker-state&code=attacker-code", nil, nil, nil)
	assertStatus(t, r, 401)
	if !strings.Contains(r.Body.String(), "INVALID_CREDENTIALS") {
		t.Fatal("callback failure lacks typed generic error")
	}
	for _, c := range r.Result().Cookies() {
		if c.Name == "fm_session" && c.Value != "" {
			t.Fatal("forged callback created session")
		}
		if c.Name == "fm_oidc" && (!c.HttpOnly || c.MaxAge != -1 || c.Path != "/api/v1/auth/oidc" || c.SameSite != http.SameSiteLaxMode) {
			t.Fatal("invalid OIDC cookie clearing")
		}
	}
}

func TestRequestCorrelationAndLogsExcludeUntrustedHeadersAndPaths(t *testing.T) {
	f := apiSetup(t)
	value := "private-value-in-untrusted-request"
	rec := f.call("GET", "/auth/me", nil, f.cookie, map[string]string{"X-Request-ID": value})
	assertStatus(t, rec, 200)
	if rec.Header().Get("X-Request-ID") == "" || rec.Header().Get("X-Request-ID") == value {
		t.Fatal("untrusted correlation ID persisted")
	}
	assertStatus(t, f.call("GET", "/execution-accounts/"+value, nil, nil, nil), 401)
	if strings.Contains(f.logs.String(), value) {
		t.Fatal("untrusted URL/header value logged")
	}
	events := decodeData[[]core.AuditEvent](t, f.call("GET", "/access/audit", nil, f.cookie, nil), 200)
	for _, event := range events {
		if strings.Contains(event.Target, value) || strings.Contains(event.RequestID, value) {
			t.Fatal("untrusted URL/header value persisted in security audit")
		}
	}
}
