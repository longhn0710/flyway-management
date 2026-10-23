package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"flyway-manager/internal/core"
)

func TestDeleteProjectAPIConfirmationMembershipRBACAndCSRF(t *testing.T) {
	f := apiSetup(t)
	p := decodeData[core.Project](t, f.call("POST", "/projects", map[string]any{"name": "Delete via API"}, f.cookie, nil), 201)
	path := "/projects/" + p.ID
	body := map[string]string{"confirmation_name": p.Name}
	assertStatus(t, f.call("DELETE", path, body, nil, nil), 401)
	for _, headers := range []map[string]string{{"Origin": "https://evil.example"}, {"X-CSRF-Token": ""}} {
		assertStatus(t, f.call("DELETE", path, body, f.cookie, headers), 403)
	}
	for _, bad := range []any{nil, map[string]string{}, map[string]string{"confirmation_name": "wrong"}, `{"confirmation_name":"Delete via API","force":true}`} {
		assertStatus(t, f.call("DELETE", path, bad, f.cookie, nil), 400)
	}
	for _, role := range []string{"Operator", "Viewer"} {
		user := decodeData[core.User](t, f.call("POST", "/access/users", map[string]string{"username": strings.ToLower(role), "password": "project-user-password-2026", "role": role}, f.cookie, nil), 201)
		login := f.call("POST", "/auth/login", map[string]string{"provider": "local", "username": user.Username, "password": "project-user-password-2026"}, nil, nil)
		identity := decodeData[core.SessionResult](t, login, 200)
		assertStatus(t, f.call("DELETE", path, body, login.Result().Cookies()[0], map[string]string{"X-CSRF-Token": identity.CSRFToken}), 403)
	}
	target := decodeData[core.Target](t, f.call("POST", "/database-targets", map[string]any{"id": "delete-target", "name": "Retained", "environment": "DEV", "host": "db.example", "port": 1521, "service": "APP", "transport": "TCP"}, f.cookie, nil), 201)
	sa := decodeData[core.ServiceAccount](t, f.call("POST", "/access/service-accounts", map[string]string{"name": "project-service", "owner_id": f.admin.ID, "purpose": "delete denial"}, f.cookie, nil), 201)
	assertStatus(t, f.call("PUT", "/access/service-accounts/"+sa.ID+"/grants", map[string]any{"grants": []core.Grant{{TargetID: target.ID, Read: true}}}, f.cookie, nil), 200)
	token := decodeData[core.IssuedToken](t, f.call("POST", "/access/service-accounts/"+sa.ID+"/tokens", map[string]string{"name": "delete-test", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, f.cookie, nil), 201)
	assertStatus(t, f.call("DELETE", path, body, nil, map[string]string{"Authorization": "Bearer " + token.Token}), 403)
	input := map[string]any{"name": p.Name, "members": []core.ProjectMember{{TargetID: target.ID}}}
	decodeData[core.Project](t, f.call("PUT", path, input, f.cookie, nil), 200)
	assertStatus(t, f.call("DELETE", path, body, f.cookie, nil), 409)
	input["members"] = []core.ProjectMember{}
	decodeData[core.Project](t, f.call("PUT", path, input, f.cookie, nil), 200)
	deleted := decodeData[map[string]bool](t, f.call("DELETE", path, body, f.cookie, nil), 200)
	if !deleted["deleted"] {
		t.Fatal("missing deletion confirmation")
	}
	for _, suffix := range []string{"", "/migration-runs"} {
		assertStatus(t, f.call("GET", path+suffix, nil, f.cookie, nil), 404)
	}
	assertStatus(t, f.call("DELETE", path, body, f.cookie, nil), 404)
	assertStatus(t, f.call("PUT", path, input, f.cookie, nil), 404)
	decodeData[core.Target](t, f.call("GET", "/database-targets/"+target.ID, nil, f.cookie, nil), 200)
	var audits int
	if e := f.db.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_events WHERE action='project.delete' AND target=? AND outcome='success'`, p.ID).Scan(&audits); e != nil || audits != 1 {
		t.Fatalf("delete audits=%d error=%v", audits, e)
	}
}
