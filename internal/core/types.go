// Package core implements identity, authorization and the Oracle migration registry.
package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
)

type Error struct {
	Status               int
	Code, Message, Field string
}

func (e *Error) Error() string            { return e.Message }
func invalid(field, message string) error { return &Error{400, "VALIDATION_ERROR", message, field} }
func denied() error                       { return &Error{403, "FORBIDDEN", "Access denied.", ""} }
func unauthorized() error                 { return &Error{401, "UNAUTHENTICATED", "Authentication required.", ""} }
func conflict(message string) error       { return &Error{409, "CONFLICT", message, ""} }
func missing() error                      { return &Error{404, "NOT_FOUND", "Resource not found.", ""} }
func translate(err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, sql.ErrNoRows) {
		return missing()
	}
	if strings.Contains(err.Error(), "constraint failed") {
		return conflict("Resource identity conflicts or a referenced resource is invalid.")
	}
	return err
}
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("cryptographic randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func id() string             { return random()[:22] }
func digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func stamp(v int64) string   { return time.Unix(v, 0).UTC().Format(time.RFC3339) }
func now() int64             { return time.Now().Unix() }

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)
var oracleName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]{0,127}$`)

func validText(v string, max int) bool {
	return strings.TrimSpace(v) != "" && len(v) <= max && !strings.ContainsAny(v, "\x00\r\n")
}

type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Provider    string `json:"provider"`
	Issuer      string `json:"issuer"`
	Subject     string `json:"subject"`
	Role        string `json:"role"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"created_at"`
}
type Actor struct {
	ID, Name, Kind, Role, SessionID, CSRF string
	ExpiresAt                             int64
}

func (a Actor) Admin() bool { return a.Kind == "user" && a.Role == "Admin" }

type Grant struct {
	TargetID string `json:"target_id"`
	SchemaID string `json:"schema_id"`
	Read     bool   `json:"read"`
	Operate  bool   `json:"operate"`
	Prod     bool   `json:"prod"`
}
type Trace struct {
	RequestID string
	Actor     Actor
}
type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func audit(ctx context.Context, q queryer, t Trace, action, target, outcome string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) VALUES(?,?,?,?,?,?,?,?)`, t.Actor.ID, t.Actor.Kind, t.Actor.Name, action, target, now(), outcome, t.RequestID)
	return err
}
func (s *Service) Event(ctx context.Context, t Trace, action, target, outcome string) error {
	return audit(ctx, s.db, t, action, target, outcome)
}
func (s *Service) mutation(ctx context.Context, t Trace, action, target string, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Recheck browser authority in the write transaction. A role/session revoked
	// after HTTP authentication must not authorize a queued administrative write.
	if t.Actor.Kind == "user" && t.Actor.SessionID != "" {
		var enabled, revoked bool
		var role string
		var expiry, last int64
		err = tx.QueryRowContext(ctx, `SELECT u.active,u.role,s.revoked,s.expires_at,s.last_used_at FROM users u JOIN sessions s ON s.user_id=u.id WHERE u.id=? AND s.id=?`, t.Actor.ID, t.Actor.SessionID).Scan(&enabled, &role, &revoked, &expiry, &last)
		if err == sql.ErrNoRows {
			return unauthorized()
		}
		if err != nil {
			return err
		}
		if !enabled || revoked || expiry <= now() || last+30*60 <= now() || role != t.Actor.Role {
			return unauthorized()
		}
	}
	if err = fn(tx); err != nil {
		return translate(err)
	}
	if err = audit(ctx, tx, t, action, target, "success"); err != nil {
		return err
	}
	return tx.Commit()
}
func requireAdmin(a Actor) error {
	if !a.Admin() {
		return denied()
	}
	return nil
}
func userScan(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created int64
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Provider, &u.Issuer, &u.Subject, &u.Role, &u.Active, &created)
	u.CreatedAt = stamp(created)
	return u, translate(err)
}

const userColumns = `id,username,display_name,provider,issuer,subject,role,active,created_at`

func validRole(r string) bool { return r == "Admin" || r == "Operator" || r == "Viewer" }
func (s *Service) User(ctx context.Context, userID string) (User, error) {
	return userScan(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id=?", userID))
}

// Allowed is the centralized policy. Global roles never imply operational PROD permission.
func Allowed(a Actor, action, targetID, schemaID, environment string, grants []Grant) bool {
	if a.Kind != "user" && a.Kind != "service" {
		return false
	}
	if a.Kind == "user" && !validRole(a.Role) {
		return false
	}
	if action == "admin" {
		return a.Admin()
	}
	if action != "read" && action != "operate" {
		return false
	}
	if targetID == "" {
		return false
	}
	if action == "read" && a.Admin() {
		return true
	}
	if action == "operate" && a.Kind == "user" && a.Role != "Admin" && a.Role != "Operator" {
		return false
	}
	for _, g := range grants {
		if g.TargetID != targetID || (g.SchemaID != "" && g.SchemaID != schemaID) {
			continue
		}
		if action == "read" && g.Read {
			return true
		}
		if action == "operate" && g.Operate && (environment != "PROD" || g.Prod) {
			return true
		}
	}
	return false
}
func (s *Service) Grants(ctx context.Context, kind, principal string) ([]Grant, error) {
	return grantsFrom(ctx, s.db, kind, principal)
}
func grantsFrom(ctx context.Context, q queryer, kind, principal string) ([]Grant, error) {
	column := "user_id"
	if kind == "service" {
		column = "service_id"
	}
	rows, err := q.QueryContext(ctx, "SELECT target_id,IFNULL(schema_id,''),can_read,can_operate,allow_prod FROM permission_grants WHERE "+column+"=? ORDER BY target_id,schema_id", principal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err = rows.Scan(&g.TargetID, &g.SchemaID, &g.Read, &g.Operate, &g.Prod); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Service) Authorize(ctx context.Context, a Actor, action, target, schema string) error {
	if action == "admin" {
		return requireAdmin(a)
	}
	var env string
	var active bool
	err := s.db.QueryRowContext(ctx, "SELECT environment,active FROM database_targets WHERE id=?", target).Scan(&env, &active)
	if err != nil {
		return translate(err)
	}
	if !active && !a.Admin() {
		return denied()
	}
	if schema != "" {
		var enabled bool
		if err = s.db.QueryRowContext(ctx, "SELECT active FROM target_schemas WHERE id=? AND target_id=?", schema, target).Scan(&enabled); err != nil {
			return missing()
		}
		if !enabled && !a.Admin() {
			return denied()
		}
	}
	gs, err := s.Grants(ctx, a.Kind, a.ID)
	if err != nil {
		return err
	}
	if !Allowed(a, action, target, schema, env, gs) {
		return denied()
	}
	return nil
}
func (s *Service) SetGrants(ctx context.Context, t Trace, kind, principal string, grants []Grant) error {
	if err := requireAdmin(t.Actor); err != nil {
		return err
	}
	if len(grants) > 1000 {
		return invalid("grants", "At most 1000 grants are supported.")
	}
	col, table := "user_id", "users"
	if kind == "service" {
		col, table = "service_id", "service_accounts"
	}
	return s.mutation(ctx, t, "permissions.update", principal, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id=?", principal).Scan(&exists); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM permission_grants WHERE "+col+"=?", principal); err != nil {
			return err
		}
		for _, g := range grants {
			if !g.Read && g.Operate || g.Prod && !g.Operate {
				return invalid("grants", "Operate requires read; PROD requires operate.")
			}
			var sid any
			if g.SchemaID != "" {
				sid = g.SchemaID
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO permission_grants("+col+",target_id,schema_id,can_read,can_operate,allow_prod) VALUES(?,?,?,?,?,?)", principal, g.TargetID, sid, g.Read, g.Operate, g.Prod); err != nil {
				return err
			}
		}
		return nil
	})
}

type Page struct {
	Limit, Offset              int
	Query, Environment, Action string
}

func (p Page) bounds() (int, int) {
	l := p.Limit
	if l < 1 {
		l = 50
	}
	if l > 200 {
		l = 200
	}
	o := p.Offset
	if o < 0 {
		o = 0
	}
	return l, o
}
