package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flyway-manager/internal/postgresql"
	"strings"
)

// ConnectionProfile is administrative input. Only its hash and policy enter
// immutable execution evidence; private filesystem paths are never API input.
type ConnectionProfile struct {
	AuthKind string `json:"auth_kind"`
	TLSMode  string `json:"tls_mode"`
	CAPEM    string `json:"ca_pem"`
	CASHA256 string `json:"ca_sha256"`
	Revision int    `json:"revision"`
}
type ProfileSnapshot struct {
	AuthKind string `json:"auth_kind"`
	TLSMode  string `json:"tls_mode"`
	CASHA256 string `json:"ca_sha256"`
	Revision int    `json:"revision"`
}

func engineOf(engine string) string {
	if engine == "" {
		return "oracle"
	}
	return engine
}

func validDBName(engine, name string) bool {
	if engine == "postgresql" {
		return postgresql.ValidIdentifier(name)
	}
	return oracleName.MatchString(name)
}
func normalizeDBName(engine, name string) string {
	name = strings.TrimSpace(name)
	if engine != "postgresql" {
		name = strings.ToUpper(name)
	}
	return name
}
func validateProfile(p *ConnectionProfile) error {
	if p == nil || p.AuthKind != "password" || p.TLSMode != "verify-full" || len(p.CAPEM) == 0 || len(p.CAPEM) > 65536 {
		return invalid("profile", "PostgreSQL requires password authentication, verify-full TLS and an approved PEM CA bundle (at most 64 KiB).")
	}
	if e := postgresql.ValidateCA(p.CAPEM); e != nil {
		return invalid("profile", e.Error())
	}
	sum := sha256.Sum256([]byte(p.CAPEM))
	p.CASHA256 = hex.EncodeToString(sum[:])
	p.Revision = 1
	return nil
}
func loadProfile(ctx context.Context, q queryer, tid string) (*ConnectionProfile, error) {
	p := &ConnectionProfile{}
	e := q.QueryRowContext(ctx, `SELECT auth_kind,tls_mode,ca_pem,ca_sha256,revision FROM target_connection_profiles WHERE target_id=?`, tid).Scan(&p.AuthKind, &p.TLSMode, &p.CAPEM, &p.CASHA256, &p.Revision)
	if e != nil {
		return nil, conflict("PostgreSQL connection profile is missing or unreadable.")
	}
	expected := p.CASHA256
	revision := p.Revision
	if e = validateProfile(p); e != nil {
		return nil, e
	}
	if p.CASHA256 != expected {
		return nil, conflict("Stored TLS trust checksum does not match its content.")
	}
	p.Revision = revision
	return p, nil
}
func saveProfile(ctx context.Context, tx *sql.Tx, tid string, p *ConnectionProfile) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO target_connection_profiles(target_id,auth_kind,tls_mode,ca_pem,ca_sha256,revision) VALUES(?,?,?,?,?,1) ON CONFLICT(target_id) DO UPDATE SET ca_pem=excluded.ca_pem,ca_sha256=excluded.ca_sha256,revision=target_connection_profiles.revision+CASE WHEN target_connection_profiles.ca_sha256<>excluded.ca_sha256 THEN 1 ELSE 0 END`, tid, p.AuthKind, p.TLSMode, p.CAPEM, p.CASHA256)
	return e
}

// Migration promotion is deliberately a server policy, never caller supplied.
// Qualified by docs/postgresql-migration-acceptance.md; changing the profile
// or lifecycle contract requires a new controlled product acceptance.
const postgresqlMigrationAccepted = true

func (s *Service) migrationEngineGate(ctx context.Context, a Actor, tid string) error {
	target, e := s.Target(ctx, a, tid)
	if e != nil {
		return e
	}
	if target.Engine == "postgresql" && !postgresqlMigrationAccepted {
		return &Error{Status: 409, Code: "ENGINE_MIGRATION_DISABLED", Message: "PostgreSQL migration is disabled pending Gate E/F acceptance."}
	}
	return nil
}

type EngineCapability struct {
	Engine     string `json:"engine"`
	Status     string `json:"status"`
	Connection bool   `json:"connection"`
	Info       bool   `json:"info"`
	Migration  bool   `json:"migration"`
	Profile    string `json:"profile"`
}

func EngineCapabilities() []EngineCapability {
	postgresqlStatus := "Connection/Info implemented; acceptance required"
	if postgresqlMigrationAccepted {
		postgresqlStatus = "Operational"
	}
	return []EngineCapability{
		{"oracle", "Operational", true, true, true, "Existing Oracle policy"},
		{"postgresql", postgresqlStatus, true, true, postgresqlMigrationAccepted, "PostgreSQL 17; password/SCRAM; verify-full; lowercase identifiers; existing schemas"},
		{"sqlserver", "Not implemented", false, false, false, ""}, {"mysql", "Not implemented", false, false, false, ""}, {"mariadb", "Not implemented", false, false, false, ""},
	}
}
