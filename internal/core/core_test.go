package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"flyway-manager/internal/database"
)

var testCtx = context.Background()

const testPassword = "correct-horse-battery-19"

type fixture struct {
	s       *Service
	db      *database.DB
	dir     string
	admin   Actor
	session string
	setup   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, e := database.Open(testCtx, dir)
	must(t, e)
	t.Cleanup(func() { db.Close() })
	s, e := New(testCtx, db, dir, "")
	must(t, e)
	b, e := os.ReadFile(s.SetupTokenFile)
	must(t, e)
	result, raw, e := s.Bootstrap(testCtx, Trace{RequestID: "bootstrap-request"}, UserInput{Username: "admin", Password: testPassword}, strings.TrimSpace(string(b)))
	must(t, e)
	a, e := s.AuthenticateSession(testCtx, raw)
	must(t, e)
	if result.User.ID != a.ID {
		t.Fatal("session identity mismatch")
	}
	return &fixture{s, db, dir, a, raw, strings.TrimSpace(string(b))}
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func status(t *testing.T, e error, want int) {
	t.Helper()
	var err *Error
	if !errors.As(e, &err) || err.Status != want {
		t.Fatalf("error %v, want status %d", e, want)
	}
}
func (f *fixture) trace() Trace { return Trace{"request-test", f.admin} }
func (f *fixture) target(t *testing.T, name, environment string) Target {
	t.Helper()
	x, e := f.s.SaveTarget(testCtx, f.trace(), "", TargetInput{ID: name, Name: name, Environment: environment, Host: name + ".example", Port: 1521, Service: "APP", Transport: "TCP"})
	must(t, e)
	return x
}
func (f *fixture) schema(t *testing.T, target, name string) Schema {
	t.Helper()
	x, e := f.s.SaveSchema(testCtx, f.trace(), target, "", SchemaInput{Name: name})
	must(t, e)
	return x
}
func (f *fixture) user(t *testing.T, name, role string) (User, Actor, string) {
	t.Helper()
	u, e := f.s.CreateUser(testCtx, f.trace(), UserInput{Username: name, Password: testPassword, Role: role})
	must(t, e)
	_, raw, e := f.s.Login(testCtx, Trace{RequestID: "user-login"}, LoginInput{"local", name, testPassword})
	must(t, e)
	a, e := f.s.AuthenticateSession(testCtx, raw)
	must(t, e)
	return u, a, raw
}

func TestPolicyDenyByDefaultAndExplicitPROD(t *testing.T) {
	if Allowed(Actor{Kind: "unknown", Role: "Admin"}, "read", "t", "s", "DEV", []Grant{{TargetID: "t", Read: true}}) {
		t.Fatal("unknown principal allowed")
	}
	admin := Actor{Kind: "user", Role: "Admin"}
	operator := Actor{Kind: "user", Role: "Operator"}
	viewer := Actor{Kind: "user", Role: "Viewer"}
	service := Actor{Kind: "service", Role: "Admin"}
	for _, a := range []Actor{admin, operator, viewer, service} {
		if Allowed(a, "operate", "t", "s", "PROD", nil) {
			t.Fatal("ungranted PROD operation")
		}
		if Allowed(a, "invented", "t", "s", "DEV", nil) {
			t.Fatal("unknown action")
		}
	}
	grants := []Grant{{"t", "s", true, true, false}}
	for _, a := range []Actor{operator, service} {
		if !Allowed(a, "read", "t", "s", "DEV", grants) || !Allowed(a, "operate", "t", "s", "DEV", grants) {
			t.Fatal("scoped grant denied")
		}
		if Allowed(a, "read", "t", "other", "DEV", grants) || Allowed(a, "read", "other", "s", "DEV", grants) {
			t.Fatal("scope escaped")
		}
		if Allowed(a, "operate", "t", "s", "PROD", grants) || Allowed(a, "admin", "", "", "", grants) {
			t.Fatal("privilege escalated")
		}
	}
	if Allowed(viewer, "operate", "t", "s", "DEV", grants) {
		t.Fatal("Viewer mutation allowed")
	}
	grants[0].Prod = true
	if !Allowed(operator, "operate", "t", "s", "PROD", grants) {
		t.Fatal("explicit PROD denied")
	}
	if !Allowed(admin, "admin", "", "", "", nil) || Allowed(service, "admin", "", "", "", nil) {
		t.Fatal("human admin separation broken")
	}
}
func TestConcurrentBootstrapAndRepeatedSetup(t *testing.T) {
	dir := t.TempDir()
	db, e := database.Open(testCtx, dir)
	must(t, e)
	defer db.Close()
	s, e := New(testCtx, db, dir, "")
	must(t, e)
	b, e := os.ReadFile(s.SetupTokenFile)
	must(t, e)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := s.Bootstrap(testCtx, Trace{RequestID: "concurrent"}, UserInput{Username: "admin", Password: testPassword}, strings.TrimSpace(string(b)))
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				success++
			} else {
				status(t, e, 409)
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("created %d initial administrators", success)
	}
	_, _, e = s.Bootstrap(testCtx, Trace{}, UserInput{Username: "evil", Password: testPassword}, "wrong-token")
	status(t, e, 403)
	var count int
	must(t, db.SQL.QueryRow("SELECT count(*) FROM users").Scan(&count))
	if count != 1 {
		t.Fatal("bootstrap duplicated users")
	}
	must(t, db.Close())
	db, e = database.Open(testCtx, dir)
	must(t, e)
	defer db.Close()
	restarted, e := New(testCtx, db, dir, "")
	must(t, e)
	required, e := restarted.BootstrapRequired(testCtx)
	must(t, e)
	if required {
		t.Fatal("setup reopened")
	}
}
func TestSessionExpirationRevocationAndDisabledUsers(t *testing.T) {
	f := newFixture(t)
	u, _, raw := f.user(t, "viewer", "Viewer")
	_, e := f.s.AuthenticateSession(testCtx, "forged")
	status(t, e, 401)
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM users WHERE password_hash LIKE '$argon2id$%' AND password_hash<>?", testPassword).Scan(new(int)))
	_, e = f.s.UpdateUser(testCtx, f.trace(), u.ID, UserUpdate{u.DisplayName, u.Role, false})
	must(t, e)
	_, e = f.s.AuthenticateSession(testCtx, raw)
	status(t, e, 401)
	_, e = f.s.UpdateUser(testCtx, f.trace(), u.ID, UserUpdate{u.DisplayName, u.Role, true})
	must(t, e)
	_, e = f.s.AuthenticateSession(testCtx, raw)
	status(t, e, 401) // reactivation never resurrects revoked sessions
	_, raw, e = f.s.Login(testCtx, Trace{}, LoginInput{"local", "viewer", testPassword})
	must(t, e)
	_, e = f.db.SQL.Exec("UPDATE sessions SET expires_at=? WHERE secret_hash=?", now()-1, digest(raw))
	must(t, e)
	_, e = f.s.AuthenticateSession(testCtx, raw)
	status(t, e, 401)
	_, raw, e = f.s.Login(testCtx, Trace{}, LoginInput{"local", "viewer", testPassword})
	must(t, e)
	_, e = f.db.SQL.Exec("UPDATE sessions SET last_used_at=? WHERE secret_hash=?", now()-31*60, digest(raw))
	must(t, e)
	_, e = f.s.AuthenticateSession(testCtx, raw)
	status(t, e, 401)
	_, raw, e = f.s.Login(testCtx, Trace{}, LoginInput{"local", "viewer", testPassword})
	must(t, e)
	a, e := f.s.AuthenticateSession(testCtx, raw)
	must(t, e)
	status(t, f.s.RevokeSession(testCtx, Trace{"cross-user", a}, f.admin.SessionID), 404)
	must(t, f.s.ChangePassword(testCtx, Trace{"password-change", a}, testPassword, "replacement-password-2026"))
	_, e = f.s.AuthenticateSession(testCtx, raw)
	status(t, e, 401)
	_, _, e = f.s.Login(testCtx, Trace{}, LoginInput{"local", "viewer", testPassword})
	status(t, e, 401)
	_, _, e = f.s.Login(testCtx, Trace{}, LoginInput{"local", "viewer", "replacement-password-2026"})
	must(t, e)
	_, _, unknown := f.s.Login(testCtx, Trace{}, LoginInput{"local", "missing", testPassword})
	_, _, wrong := f.s.Login(testCtx, Trace{}, LoginInput{"local", "viewer", "bad"})
	if unknown.Error() != wrong.Error() {
		t.Fatal("enumerable authentication errors")
	}
	_, e = f.s.UpdateUser(testCtx, f.trace(), f.admin.ID, UserUpdate{"Admin", "Viewer", false})
	status(t, e, 409)
}
func TestScopedRegistryAndSchemaAccountInvariants(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, "db-uat", "UAT")
	other := f.target(t, "db-other", "PROD")
	a := f.schema(t, target.ID, "CORE_BANKING")
	b := f.schema(t, target.ID, "PAYMENT")
	c := f.schema(t, other.ID, "CORE_BANKING")
	u, operator, _ := f.user(t, "operator", "Operator")
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", u.ID, []Grant{{target.ID, a.ID, true, true, false}}))
	targets, e := f.s.Targets(testCtx, operator, Page{})
	must(t, e)
	if len(targets) != 1 || targets[0].ID != target.ID {
		t.Fatal("target scope bypass")
	}
	schemas, e := f.s.Schemas(testCtx, operator, target.ID, Page{})
	must(t, e)
	if len(schemas) != 1 || schemas[0].ID != a.ID {
		t.Fatal("schema list bypass")
	}
	_, e = f.s.Schema(testCtx, operator, target.ID, b.ID)
	status(t, e, 403)
	_, e = f.s.Schema(testCtx, operator, target.ID, c.ID)
	status(t, e, 404)
	_, e = f.s.SaveTarget(testCtx, Trace{"operator", operator}, "", TargetInput{})
	status(t, e, 403)
	_, e = f.s.SaveSchema(testCtx, f.trace(), target.ID, "", SchemaInput{Name: "DUPLICATE_HISTORY", HistorySchema: a.Name})
	status(t, e, 409)
	account, e := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: target.ID, Name: "Deploy", Username: "DEPLOY_ADMIN", SchemaIDs: []string{a.ID, b.ID}, Password: "oracle-secret"})
	must(t, e)
	if account.Username == a.Name || len(account.SchemaIDs) != 2 {
		t.Fatal("execution identity incorrectly equated to schema")
	}
	_, e = f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: target.ID, Name: "Cross", Username: "OTHER", SchemaIDs: []string{c.ID}})
	status(t, e, 409)
	a, e = f.s.SaveSchema(testCtx, f.trace(), target.ID, a.ID, SchemaInput{Name: a.Name, DefaultAccountID: account.ID, Active: true})
	must(t, e)
	_, e = f.s.SaveAccount(testCtx, f.trace(), account.ID, AccountInput{Name: account.Name, Username: account.Username, Active: true, SchemaIDs: []string{b.ID}})
	status(t, e, 409)
	// Invalid grant batch is atomic and does not erase existing access.
	e = f.s.SetGrants(testCtx, f.trace(), "user", u.ID, []Grant{{target.ID, c.ID, true, true, true}})
	status(t, e, 409)
	gs, e := f.s.Grants(testCtx, "user", u.ID)
	must(t, e)
	if len(gs) != 1 || gs[0].SchemaID != a.ID {
		t.Fatal("partial grant write")
	}
	gs = []Grant{{other.ID, c.ID, true, true, false}}
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", u.ID, gs))
	status(t, f.s.Authorize(testCtx, operator, "operate", other.ID, c.ID), 403)
	gs[0].Prod = true
	must(t, f.s.SetGrants(testCtx, f.trace(), "user", u.ID, gs))
	must(t, f.s.Authorize(testCtx, operator, "operate", other.ID, c.ID))
}
func TestCredentialsRestartRotationRevocationAndKeyFailures(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, "db", "DEV")
	schema := f.schema(t, target.ID, "CORE_BANKING")
	account, e := f.s.SaveAccount(testCtx, f.trace(), "", AccountInput{TargetID: target.ID, Name: "deployer", Username: "DEPLOY_ADMIN", SchemaIDs: []string{schema.ID}, Password: "oracle-password-1"})
	must(t, e)
	var first []byte
	must(t, f.db.SQL.QueryRow("SELECT payload FROM credential_secrets WHERE account_id=?", account.ID).Scan(&first))
	if strings.Contains(string(first), "oracle-password") {
		t.Fatal("plaintext credential stored")
	}
	data, e := json.Marshal(account)
	must(t, e)
	if strings.Contains(string(data), "oracle-password") || strings.Contains(string(data), "payload") {
		t.Fatal("secret returned")
	}
	_, e = f.s.ReplaceCredential(testCtx, f.trace(), account.ID, "oracle-password-2", false)
	must(t, e)
	var second []byte
	must(t, f.db.SQL.QueryRow("SELECT payload FROM credential_secrets WHERE account_id=?", account.ID).Scan(&second))
	if string(first) == string(second) {
		t.Fatal("nonce reused")
	}
	installation := f.db.InstallationID
	must(t, f.db.Close())
	db, e := database.Open(testCtx, f.dir)
	must(t, e)
	defer db.Close()
	s, e := New(testCtx, db, f.dir, "")
	must(t, e)
	if db.InstallationID != installation {
		t.Fatal("installation replaced")
	}
	plain, e := s.vault.Open("credential:"+account.ID, second)
	must(t, e)
	if string(plain) != "oracle-password-2" {
		t.Fatal("rotation failed after restart")
	}
	clear(plain)
	_, e = s.vault.Open("credential:another-account", second)
	if e == nil {
		t.Fatal("record substitution accepted")
	}
	meta, e := s.ReplaceCredential(testCtx, f.trace(), account.ID, "", true)
	must(t, e)
	if !meta.Revoked || meta.Configured || meta.Version != 3 {
		t.Fatal("revoke metadata incorrect")
	}
	keyPath := filepath.Join(f.dir, "secrets", "master.key")
	key, e := os.ReadFile(keyPath)
	must(t, e)
	externalKey := filepath.Join(f.dir, "external-master.key")
	must(t, os.WriteFile(externalKey, make([]byte, 32), 0600))
	_, e = NewWithMasterKeyFile(testCtx, db, f.dir, "", externalKey)
	if e == nil || !strings.Contains(e.Error(), "wrong master key") {
		t.Fatalf("wrong external key accepted: %v", e)
	}
	_, e = NewWithMasterKeyFile(testCtx, db, f.dir, "", filepath.Join(f.dir, "missing-external-key"))
	if e == nil || !strings.Contains(e.Error(), "external master key file is missing") {
		t.Fatalf("missing external key accepted: %v", e)
	}
	must(t, os.WriteFile(keyPath, make([]byte, 32), 0600))
	_, e = New(testCtx, db, f.dir, "")
	if e == nil || !strings.Contains(e.Error(), "wrong master key") {
		t.Fatalf("wrong key accepted: %v", e)
	}
	must(t, os.Remove(keyPath))
	_, e = New(testCtx, db, f.dir, "")
	if e == nil || !strings.Contains(e.Error(), "master key missing") {
		t.Fatal("missing key silently replaced")
	}
	if _, e = os.Stat(keyPath); !os.IsNotExist(e) {
		t.Fatal("missing key regenerated")
	}
	must(t, os.WriteFile(keyPath, key, 0600))
	events, e := s.Audit(testCtx, f.admin, Page{Limit: 200})
	must(t, e)
	for _, event := range events {
		if strings.Contains(event.Target, "oracle-password") || event.ActorID == "" || event.RequestID == "" {
			t.Fatal("invalid audit attribution or secret leakage")
		}
	}
}

func TestFreshExternalKeyInstallationCreatesBootstrapDirectory(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	keyPath := filepath.Join(dir, "mounted", "master.key")
	must(t, os.MkdirAll(filepath.Dir(keyPath), 0700))
	must(t, os.WriteFile(keyPath, make([]byte, 32), 0600))
	db, err := database.Open(testCtx, dataDir)
	must(t, err)
	defer db.Close()
	s, err := NewWithMasterKeyFile(testCtx, db, dataDir, "", keyPath)
	must(t, err)
	if _, err := os.Stat(s.SetupTokenFile); err != nil {
		t.Fatalf("external-key startup failed to create bootstrap token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "secrets", "master.key")); !os.IsNotExist(err) {
		t.Fatalf("external-key startup created a local replacement key: %v", err)
	}
}
func TestServiceTokenAuthenticationScopesRotationAndExpiry(t *testing.T) {
	f := newFixture(t)
	target := f.target(t, "db", "DEV")
	schema := f.schema(t, target.ID, "CORE_BANKING")
	service, e := f.s.SaveService(testCtx, f.trace(), "", ServiceInput{Name: "ci", OwnerID: f.admin.ID, Purpose: "deploy"})
	must(t, e)
	_, e = f.s.IssueToken(testCtx, f.trace(), service.ID, "", TokenInput{Name: "build", ExpiresAt: stamp(now() + 3600)})
	status(t, e, 400)
	must(t, f.s.SetGrants(testCtx, f.trace(), "service", service.ID, []Grant{{target.ID, schema.ID, true, true, false}}))
	token, e := f.s.IssueToken(testCtx, f.trace(), service.ID, "", TokenInput{Name: "build", ExpiresAt: stamp(now() + 3600)})
	must(t, e)
	a, e := f.s.AuthenticateToken(testCtx, token.Token, "token-request")
	must(t, e)
	if a.Kind != "service" {
		t.Fatal("service turned into user")
	}
	must(t, f.s.Authorize(testCtx, a, "operate", target.ID, schema.ID))
	status(t, f.s.Authorize(testCtx, a, "admin", "", ""), 403)
	_, e = f.s.Me(testCtx, a)
	status(t, e, 403)
	records, e := f.s.Tokens(testCtx, f.admin, service.ID)
	must(t, e)
	b, e := json.Marshal(records)
	must(t, e)
	if strings.Contains(string(b), token.Token) {
		t.Fatal("token readback")
	}
	var stored string
	must(t, f.db.SQL.QueryRow("SELECT secret_hash FROM api_tokens WHERE id=?", token.Metadata.ID).Scan(&stored))
	if stored == token.Token || stored != digest(token.Token) {
		t.Fatal("token hash not persisted")
	}
	rotated, e := f.s.IssueToken(testCtx, f.trace(), service.ID, token.Metadata.ID, TokenInput{ExpiresAt: stamp(now() + 3600)})
	must(t, e)
	_, e = f.s.AuthenticateToken(testCtx, token.Token, "old")
	status(t, e, 401)
	_, e = f.s.IssueToken(testCtx, f.trace(), service.ID, token.Metadata.ID, TokenInput{ExpiresAt: stamp(now() + 3600)})
	status(t, e, 409)
	must(t, f.s.RevokeToken(testCtx, f.trace(), service.ID, rotated.Metadata.ID))
	_, e = f.s.AuthenticateToken(testCtx, rotated.Token, "revoked")
	status(t, e, 401)
	expired, e := f.s.IssueToken(testCtx, f.trace(), service.ID, "", TokenInput{Name: "expiry", ExpiresAt: stamp(now() + 3600)})
	must(t, e)
	_, e = f.db.SQL.Exec("UPDATE api_tokens SET expires_at=? WHERE id=?", now()-1, expired.Metadata.ID)
	must(t, e)
	_, e = f.s.AuthenticateToken(testCtx, expired.Token, "expired")
	status(t, e, 401)
}
func TestAtomicAuditFailureRollsBackMutationAndExternalUsersAreExplicit(t *testing.T) {
	f := newFixture(t)
	_, e := f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "sso", Provider: "oidc", Issuer: "https://issuer.example", Subject: "stable-subject", Role: "Viewer"})
	must(t, e)
	_, e = f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "duplicate", Provider: "oidc", Issuer: "https://issuer.example", Subject: "stable-subject"})
	status(t, e, 409)
	_, _, e = f.s.Login(testCtx, Trace{}, LoginInput{"ldap", "admin", testPassword})
	status(t, e, 401) // no local fallback
	_, e = f.db.SQL.Exec("CREATE TRIGGER deny_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END")
	must(t, e)
	_, e = f.s.SaveTarget(testCtx, f.trace(), "", TargetInput{ID: "atomic", Name: "atomic", Environment: "DEV", Host: "atomic.example", Port: 1521, Service: "APP", Transport: "TCP"})
	if e == nil {
		t.Fatal("unaudited mutation committed")
	}
	var n int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM database_targets WHERE id='atomic'").Scan(&n))
	if n != 0 {
		t.Fatal("mutation escaped audit transaction")
	}
}
func TestOIDCStateBindingReplayAndExpiry(t *testing.T) {
	f := newFixture(t)
	// Challenge validation occurs before any external call; a forged callback cannot log in.
	_, _, e := f.s.OIDCCallback(testCtx, Trace{}, random(), random(), "code")
	status(t, e, 401)
	state, cookie := random(), random()
	flow := oidcFlow{random(), random(), digest(cookie), "unused"}
	payload, e := f.s.sealJSON("oidc:"+digest(state), flow)
	must(t, e)
	_, e = f.db.SQL.Exec("INSERT INTO oidc_flows VALUES(?,?,?)", digest(state), payload, now()+300)
	must(t, e)
	_, _, e = f.s.OIDCCallback(testCtx, Trace{}, state, random(), "code")
	status(t, e, 401)
	var count int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM oidc_flows").Scan(&count))
	if count != 0 {
		t.Fatal("challenge not consumed")
	}
	_, _, e = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "code")
	status(t, e, 401)
	state = random()
	payload, e = f.s.sealJSON("oidc:"+digest(state), flow)
	must(t, e)
	_, e = f.db.SQL.Exec("INSERT INTO oidc_flows VALUES(?,?,?)", digest(state), payload, now()-1)
	must(t, e)
	_, _, e = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "code")
	status(t, e, 401)
}
func TestRateLimiterBoundedAndPasswordCancellation(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 10; i++ {
		if !f.s.RateAllow("test") {
			t.Fatal("premature throttle")
		}
	}
	if f.s.RateAllow("test") {
		t.Fatal("rate limit absent")
	}
	ctx, cancel := context.WithCancel(testCtx)
	cancel()
	f.s.passwordSlots <- struct{}{}
	f.s.passwordSlots <- struct{}{}
	_, e := f.s.hashPassword(ctx, testPassword)
	if !errors.Is(e, context.Canceled) {
		t.Fatal("password work ignored cancellation")
	}
	<-f.s.passwordSlots
	<-f.s.passwordSlots
	var hash string
	must(t, f.db.SQL.QueryRow("SELECT password_hash FROM users WHERE id=?", f.admin.ID).Scan(&hash))
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatal("weak password parameters")
	}
	if !f.s.passwordMatches(testCtx, testPassword, hash) || f.s.passwordMatches(testCtx, "bad", hash) {
		t.Fatal("password verification broken")
	}
}

func TestRevocationPreventsAlreadyAuthenticatedAdministrativeWrite(t *testing.T) {
	f := newFixture(t)
	u, other, _ := f.user(t, "other-admin", "Admin")
	_, e := f.s.UpdateUser(testCtx, f.trace(), u.ID, UserUpdate{DisplayName: "Other admin", Role: "Admin", Active: false})
	must(t, e)
	_, e = f.s.CreateUser(testCtx, Trace{"stale-admin", other}, UserInput{Username: "forged-after-disable", Password: testPassword})
	status(t, e, 401)
	var count int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM users WHERE username='forged-after-disable'").Scan(&count))
	if count != 0 {
		t.Fatal("revoked actor committed a write")
	}
}
