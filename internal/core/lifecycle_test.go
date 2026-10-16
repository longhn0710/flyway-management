package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"flyway-manager/internal/database"
)

func TestOwnerDeactivationAndTransferPermanentlyRevokeServiceTokens(t *testing.T) {
	f := newFixture(t)
	owner, _, _ := f.user(t, "service-owner", "Viewer")
	nextOwner, _, _ := f.user(t, "next-owner", "Viewer")
	target := f.target(t, "service-db", "DEV")
	schema := f.schema(t, target.ID, "CORE")
	service, err := f.s.SaveService(testCtx, f.trace(), "", ServiceInput{Name: "deployment", OwnerID: owner.ID, Purpose: "automated deployment"})
	must(t, err)
	must(t, f.s.SetGrants(testCtx, f.trace(), "service", service.ID, []Grant{{TargetID: target.ID, SchemaID: schema.ID, Read: true, Operate: true}}))

	first, err := f.s.IssueToken(testCtx, f.trace(), service.ID, "", TokenInput{Name: "deploy", ExpiresAt: stamp(now() + 3600)})
	must(t, err)
	_, err = f.s.AuthenticateToken(testCtx, first.Token, "before-disable")
	must(t, err)
	_, err = f.s.UpdateUser(testCtx, f.trace(), owner.ID, UserUpdate{DisplayName: owner.DisplayName, Role: owner.Role, Active: false})
	must(t, err)
	_, err = f.s.AuthenticateToken(testCtx, first.Token, "while-disabled")
	status(t, err, 401)
	_, err = f.s.UpdateUser(testCtx, f.trace(), owner.ID, UserUpdate{DisplayName: owner.DisplayName, Role: owner.Role, Active: true})
	must(t, err)
	_, err = f.s.AuthenticateToken(testCtx, first.Token, "after-reactivation")
	status(t, err, 401)

	second, err := f.s.IssueToken(testCtx, f.trace(), service.ID, "", TokenInput{Name: "deploy", ExpiresAt: stamp(now() + 3600)})
	must(t, err)
	_, err = f.s.AuthenticateToken(testCtx, second.Token, "before-transfer")
	must(t, err)
	_, err = f.s.SaveService(testCtx, f.trace(), service.ID, ServiceInput{Name: service.Name, OwnerID: nextOwner.ID, Purpose: service.Purpose, Active: true})
	must(t, err)
	_, err = f.s.AuthenticateToken(testCtx, second.Token, "after-transfer")
	status(t, err, 401)
	tokens, err := f.s.Tokens(testCtx, f.admin, service.ID)
	must(t, err)
	if len(tokens) != 2 || !tokens[0].Revoked || !tokens[1].Revoked {
		t.Fatalf("owner lifecycle left tokens unrevoked: %#v", tokens)
	}
}

func TestReachableAdministratorLifecycleWithExternalProvider(t *testing.T) {
	f := newFixture(t)
	issuer := "ldaps://directory.example:636"
	external, err := f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "external-admin", Provider: "ldap", Issuer: issuer, Subject: "immutable-admin-id", Role: "Admin"})
	must(t, err)
	ldap := LDAPSettings{Enabled: true, URL: issuer, BaseDN: "dc=example,dc=com", BindDNTemplate: "uid={username},ou=people,dc=example,dc=com", UsernameAttribute: "uid", SubjectAttribute: "entryUUID"}
	_, err = f.s.UpdateSettings(testCtx, f.trace(), AuthSettings{LocalEnabled: false, LDAP: ldap})
	must(t, err)

	_, err = f.s.UpdateUser(testCtx, f.trace(), external.ID, UserUpdate{DisplayName: external.DisplayName, Role: "Viewer", Active: true})
	status(t, err, 409)
	_, err = f.s.UpdateUser(testCtx, f.trace(), external.ID, UserUpdate{DisplayName: external.DisplayName, Role: "Admin", Active: false})
	status(t, err, 409)
	unchanged, err := f.s.User(testCtx, external.ID)
	must(t, err)
	if !unchanged.Active || unchanged.Role != "Admin" {
		t.Fatalf("failed final-admin mutation changed user: %#v", unchanged)
	}

	_, err = f.s.UpdateSettings(testCtx, f.trace(), AuthSettings{LocalEnabled: true, LDAP: ldap})
	must(t, err)
	_, err = f.s.UpdateUser(testCtx, f.trace(), external.ID, UserUpdate{DisplayName: external.DisplayName, Role: "Admin", Active: false})
	must(t, err)
	_, err = f.s.UpdateUser(testCtx, f.trace(), f.admin.ID, UserUpdate{DisplayName: f.admin.Name, Role: "Viewer", Active: true})
	status(t, err, 409)
	_, err = f.s.UpdateUser(testCtx, f.trace(), f.admin.ID, UserUpdate{DisplayName: f.admin.Name, Role: "Admin", Active: false})
	status(t, err, 409)
}

func TestAuthenticationProviderSecretIsEncryptedAndBlankUpdatePreservesIt(t *testing.T) {
	f := newFixture(t)
	secret := "provider-client-secret-must-not-persist-in-plaintext"
	settings := AuthSettings{LocalEnabled: true, OIDC: OIDCSettings{ClientSecret: secret}}
	_, err := f.s.UpdateSettings(testCtx, f.trace(), settings)
	must(t, err)
	var payload []byte
	must(t, f.db.SQL.QueryRow("SELECT payload FROM auth_settings WHERE id=1").Scan(&payload))
	if strings.Contains(string(payload), secret) {
		t.Fatal("provider secret persisted in plaintext")
	}
	public, err := f.s.PublicSettings(testCtx, f.admin)
	must(t, err)
	if public.OIDC.ClientSecret != "" || !public.OIDC.ClientSecretConfigured {
		t.Fatalf("public settings leaked or lost secret state: %#v", public.OIDC)
	}
	encoded, err := json.Marshal(public)
	must(t, err)
	if strings.Contains(string(encoded), secret) {
		t.Fatal("provider secret appeared in public settings")
	}

	// Saving the returned write-only form must preserve the existing secret.
	_, err = f.s.UpdateSettings(testCtx, f.trace(), public)
	must(t, err)
	persisted, err := f.s.Settings(testCtx)
	must(t, err)
	if persisted.OIDC.ClientSecret != secret {
		t.Fatal("blank secret update cleared stored provider secret")
	}

	must(t, f.db.Close())
	db, err := database.Open(testCtx, f.dir)
	must(t, err)
	defer db.Close()
	restarted, err := New(testCtx, db, f.dir, "")
	must(t, err)
	persisted, err = restarted.Settings(testCtx)
	must(t, err)
	if persisted.OIDC.ClientSecret != secret {
		t.Fatal("provider secret did not survive restart")
	}
	public, err = restarted.PublicSettings(testCtx, f.admin)
	must(t, err)
	if public.OIDC.ClientSecret != "" || !public.OIDC.ClientSecretConfigured {
		t.Fatal("restart exposed provider secret")
	}
}

func TestOversizedCurrentPasswordIsRejectedBeforePasswordVerification(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(testCtx)
	cancel()
	f.s.passwordSlots <- struct{}{}
	f.s.passwordSlots <- struct{}{}
	defer func() { <-f.s.passwordSlots; <-f.s.passwordSlots }()
	err := f.s.ChangePassword(ctx, f.trace(), strings.Repeat("x", 1025), "replacement-password-2026")
	status(t, err, 400)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Field != "current_password" {
		t.Fatalf("wrong validation error: %v", err)
	}
}
