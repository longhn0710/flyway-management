package httpapi

import (
	"flyway-manager/internal/core"
	"strings"
	"testing"
)

func TestConnectionRoutesRejectAnonymousOperatorsAndCSRF(t *testing.T) {
	f := apiSetup(t)
	input := map[string]any{"target": map[string]any{"name": "Test", "environment": "DEV", "host": "127.0.0.1", "port": 1, "service": "APP", "transport": "TCP"}, "username": "APP", "password": "never-log-this-password"}
	for _, path := range []string{"/connections/test", "/connections", "/connections/nonexistent/test", "/connections/nonexistent"} {
		method := "POST"
		if path == "/connections/nonexistent" {
			method = "PUT"
		}
		assertStatus(t, f.call(method, path, input, nil, nil), 401)
		assertStatus(t, f.call(method, path, input, f.cookie, map[string]string{"X-CSRF-Token": "wrong"}), 403)
	}
	user := decodeData[core.User](t, f.call("POST", "/access/users", map[string]any{"username": "operator", "password": "operator-test-password", "role": "Operator"}, f.cookie, nil), 201)
	login := f.call("POST", "/auth/login", map[string]any{"provider": "local", "username": user.Username, "password": "operator-test-password"}, nil, nil)
	session := decodeData[core.SessionResult](t, login, 200)
	oldCookie, oldCSRF := f.cookie, f.csrf
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == "fm_session" {
			f.cookie = cookie
		}
	}
	f.csrf = session.CSRFToken
	assertStatus(t, f.call("POST", "/connections/test", input, f.cookie, nil), 403)
	assertStatus(t, f.call("POST", "/connections", input, f.cookie, nil), 403)
	assertStatus(t, f.call("GET", "/connections/nonexistent", nil, f.cookie, nil), 403)
	f.cookie, f.csrf = oldCookie, oldCSRF
	result := f.call("POST", "/connections/test", input, f.cookie, nil)
	assertStatus(t, result, 200)
	if strings.Contains(result.Body.String(), "never-log-this-password") || strings.Contains(f.logs.String(), "never-log-this-password") {
		t.Fatal("probe leaked password")
	}
	var count int
	if e := f.db.SQL.QueryRow("SELECT count(*) FROM database_targets").Scan(&count); e != nil || count != 0 {
		t.Fatal("test persisted registry", e, count)
	}
}
func TestConnectionAllowlistEnforcedThroughDirectWorkflowAPIs(t *testing.T) {
	f := apiSetup(t)
	target := decodeData[core.Target](t, f.call("POST", "/database-targets", map[string]any{"id": "db", "name": "DB", "host": "127.0.0.1", "port": 1, "service": "APP", "environment": "DEV", "transport": "TCP"}, f.cookie, nil), 201)
	allowed := decodeData[core.Schema](t, f.call("POST", "/database-targets/db/schemas", map[string]string{"name": "APP"}, f.cookie, nil), 201)
	denied := decodeData[core.Schema](t, f.call("POST", "/database-targets/db/schemas", map[string]string{"name": "OTHER"}, f.cookie, nil), 201)
	account := decodeData[core.ExecutionAccount](t, f.call("POST", "/execution-accounts", map[string]any{"target_id": target.ID, "name": "APP", "username": "APP", "password": "private-oracle-password", "schema_ids": []string{allowed.ID, denied.ID}}, f.cookie, nil), 201)
	decodeData[[]core.Grant](t, f.call("PUT", "/access/users/"+f.admin.ID+"/grants", map[string]any{"grants": []core.Grant{{TargetID: target.ID, Read: true, Operate: true}}}, f.cookie, nil), 200)
	if _, e := f.db.SQL.Exec("INSERT INTO connection_settings VALUES(?,?,20)", target.ID, account.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.SQL.Exec("INSERT INTO connection_managed_accounts VALUES(?,?)", target.ID, account.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.SQL.Exec("INSERT INTO connection_allowed_schemas VALUES(?,?,?,'potential',0)", target.ID, account.ID, allowed.ID); e != nil {
		t.Fatal(e)
	}
	visible := decodeData[[]core.Schema](t, f.call("GET", "/migration-connections/db/schemas", nil, f.cookie, nil), 200)
	if len(visible) != 1 || visible[0].ID != allowed.ID {
		t.Fatal(visible)
	}
	assertStatus(t, f.call("GET", "/database-targets/db/schemas/"+denied.ID+"/execution-accounts", nil, f.cookie, nil), 403)
	input := map[string]any{"target_id": target.ID, "schema_id": denied.ID, "execution_account_id": account.ID, "artifact_id": "fake"}
	assertStatus(t, f.call("POST", "/migration-preflight", input, f.cookie, nil), 403)
	assertStatus(t, f.call("POST", "/migration-runs", input, f.cookie, map[string]string{"Idempotency-Key": "allowlist-negative"}), 403)
	// The registry/history identity remains readable after selection changes.
	decodeData[core.Schema](t, f.call("GET", "/database-targets/db/schemas/"+denied.ID, nil, f.cookie, nil), 200)
}
