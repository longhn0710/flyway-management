package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type ServiceAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"`
	Purpose   string `json:"purpose"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}
type ServiceInput struct {
	Name    string `json:"name"`
	OwnerID string `json:"owner_id"`
	Purpose string `json:"purpose"`
	Active  bool   `json:"active"`
}

func serviceScan(row interface{ Scan(...any) error }) (ServiceAccount, error) {
	var x ServiceAccount
	var c int64
	e := row.Scan(&x.ID, &x.Name, &x.OwnerID, &x.Purpose, &x.Active, &c)
	x.CreatedAt = stamp(c)
	return x, translate(e)
}
func (s *Service) ServiceAccount(ctx context.Context, a Actor, uid string) (ServiceAccount, error) {
	if e := requireAdmin(a); e != nil {
		return ServiceAccount{}, e
	}
	return serviceScan(s.db.QueryRowContext(ctx, "SELECT id,name,owner_id,purpose,active,created_at FROM service_accounts WHERE id=?", uid))
}
func (s *Service) ServiceAccounts(ctx context.Context, a Actor, p Page) ([]ServiceAccount, error) {
	if e := requireAdmin(a); e != nil {
		return nil, e
	}
	l, o := p.bounds()
	rows, e := s.db.QueryContext(ctx, "SELECT id,name,owner_id,purpose,active,created_at FROM service_accounts WHERE name LIKE ? ORDER BY name,id LIMIT ? OFFSET ?", "%"+p.Query+"%", l, o)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ServiceAccount{}
	for rows.Next() {
		x, e := serviceScan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) SaveService(ctx context.Context, t Trace, uid string, in ServiceInput) (ServiceAccount, error) {
	if e := requireAdmin(t.Actor); e != nil {
		return ServiceAccount{}, e
	}
	in.Name = strings.TrimSpace(in.Name)
	if !identifier.MatchString(in.Name) || !validText(in.Purpose, 500) {
		return ServiceAccount{}, invalid("service_account", "A simple name, purpose and active owner are required.")
	}
	create := uid == ""
	action := "service_account.update"
	if create {
		uid = id()
		in.Active = true
		action = "service_account.create"
	}
	e := s.mutation(ctx, t, action, uid, func(tx *sql.Tx) error {
		var ownerActive bool
		if e := tx.QueryRowContext(ctx, "SELECT active FROM users WHERE id=?", in.OwnerID).Scan(&ownerActive); e != nil {
			return e
		}
		if !ownerActive {
			return invalid("owner_id", "Owner must be an active application user.")
		}
		if create {
			_, e := tx.ExecContext(ctx, "INSERT INTO service_accounts(id,name,owner_id,purpose,active,created_at) VALUES(?,?,?,?,1,?)", uid, in.Name, in.OwnerID, in.Purpose, now())
			return e
		}
		var oldOwner string
		if e := tx.QueryRowContext(ctx, "SELECT owner_id FROM service_accounts WHERE id=?", uid).Scan(&oldOwner); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE service_accounts SET name=?,owner_id=?,purpose=?,active=? WHERE id=?", in.Name, in.OwnerID, in.Purpose, in.Active, uid)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return missing()
		}
		if !in.Active || oldOwner != in.OwnerID {
			if _, e = tx.ExecContext(ctx, "UPDATE api_tokens SET revoked=1 WHERE service_id=?", uid); e != nil {
				return e
			}
			e = audit(ctx, tx, t, "token.revoke_service", uid, "success")
		}
		return e
	})
	if e != nil {
		return ServiceAccount{}, e
	}
	return s.ServiceAccount(ctx, t.Actor, uid)
}

type Token struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
	LastUsedAt string `json:"last_used_at"`
	Revoked    bool   `json:"revoked"`
}
type TokenInput struct {
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at"`
}
type IssuedToken struct {
	Token    string `json:"token"`
	Metadata Token  `json:"metadata"`
}

func tokenScan(row interface{ Scan(...any) error }) (Token, error) {
	var x Token
	var c, e int64
	var l sql.NullInt64
	err := row.Scan(&x.ID, &x.Name, &x.Prefix, &c, &e, &l, &x.Revoked)
	x.CreatedAt = stamp(c)
	x.ExpiresAt = stamp(e)
	if l.Valid {
		x.LastUsedAt = stamp(l.Int64)
	}
	return x, translate(err)
}
func (s *Service) Tokens(ctx context.Context, a Actor, serviceID string) ([]Token, error) {
	if _, e := s.ServiceAccount(ctx, a, serviceID); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, "SELECT id,name,prefix,created_at,expires_at,last_used_at,revoked FROM api_tokens WHERE service_id=? ORDER BY created_at DESC,id", serviceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		x, e := tokenScan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) IssueToken(ctx context.Context, t Trace, serviceID, rotateID string, in TokenInput) (IssuedToken, error) {
	if e := requireAdmin(t.Actor); e != nil {
		return IssuedToken{}, e
	}
	ex, e := time.Parse(time.RFC3339, in.ExpiresAt)
	if e != nil || ex.Unix() <= now() || ex.Unix() > now()+366*86400 {
		return IssuedToken{}, invalid("expires_at", "Token expiry must be in the next 366 days.")
	}
	if rotateID == "" && !validText(in.Name, 100) {
		return IssuedToken{}, invalid("name", "Token name is required (max 100 bytes).")
	}
	raw := "fm_" + random()
	tid := id()
	action := "token.issue"
	if rotateID != "" {
		action = "token.rotate"
	}
	e = s.mutation(ctx, t, action, tid, func(tx *sql.Tx) error {
		var active, owner bool
		if e := tx.QueryRowContext(ctx, "SELECT a.active,u.active FROM service_accounts a JOIN users u ON u.id=a.owner_id WHERE a.id=?", serviceID).Scan(&active, &owner); e != nil {
			return e
		}
		if !active || !owner {
			return conflict("Service account and owner must be active.")
		}
		gs, e := grantsFrom(ctx, tx, "service", serviceID)
		if e != nil {
			return e
		}
		if len(gs) == 0 {
			return invalid("grants", "Assign explicit service-account permissions before issuing a token.")
		}
		if rotateID != "" {
			var revoked bool
			if e := tx.QueryRowContext(ctx, "SELECT name,revoked FROM api_tokens WHERE id=? AND service_id=?", rotateID, serviceID).Scan(&in.Name, &revoked); e != nil {
				return e
			}
			if revoked {
				return conflict("Token has already been revoked or rotated.")
			}
			if _, e = tx.ExecContext(ctx, "UPDATE api_tokens SET revoked=1 WHERE id=?", rotateID); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO api_tokens(id,service_id,name,prefix,secret_hash,created_at,expires_at) VALUES(?,?,?,?,?,?,?)", tid, serviceID, in.Name, raw[:11], digest(raw), now(), ex.Unix())
		return e
	})
	if e != nil {
		return IssuedToken{}, e
	}
	meta, e := tokenScan(s.db.QueryRowContext(ctx, "SELECT id,name,prefix,created_at,expires_at,last_used_at,revoked FROM api_tokens WHERE id=?", tid))
	return IssuedToken{raw, meta}, e
}
func (s *Service) RevokeToken(ctx context.Context, t Trace, serviceID, tid string) error {
	if e := requireAdmin(t.Actor); e != nil {
		return e
	}
	return s.mutation(ctx, t, "token.revoke", tid, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, "UPDATE api_tokens SET revoked=1 WHERE id=? AND service_id=?", tid, serviceID)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return missing()
		}
		return nil
	})
}
func (s *Service) AuthenticateToken(ctx context.Context, raw, requestID string) (Actor, error) {
	if len(raw) != 46 || !strings.HasPrefix(raw, "fm_") {
		return Actor{}, unauthorized()
	}
	var a Actor
	var tid string
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return a, e
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `SELECT a.id,a.name,t.id,t.expires_at FROM api_tokens t JOIN service_accounts a ON a.id=t.service_id JOIN users u ON u.id=a.owner_id WHERE t.secret_hash=? AND t.revoked=0 AND t.expires_at>? AND a.active=1 AND u.active=1`, digest(raw), now()).Scan(&a.ID, &a.Name, &tid, &a.ExpiresAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Actor{}, unauthorized()
	}
	if e != nil {
		return Actor{}, e
	}
	a.Kind = "service"
	a.Role = "Service"
	if _, e = tx.ExecContext(ctx, "UPDATE api_tokens SET last_used_at=? WHERE id=?", now(), tid); e != nil {
		return Actor{}, e
	}
	if e = audit(ctx, tx, Trace{requestID, a}, "auth.token", tid, "success"); e != nil {
		return Actor{}, e
	}
	e = tx.Commit()
	return a, e
}

type AuditEvent struct {
	ID        int64  `json:"id"`
	ActorID   string `json:"actor_id"`
	ActorType string `json:"actor_type"`
	ActorName string `json:"actor_name"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Timestamp string `json:"timestamp"`
	Outcome   string `json:"outcome"`
	RequestID string `json:"request_id"`
}

func (s *Service) Audit(ctx context.Context, a Actor, p Page) ([]AuditEvent, error) {
	if e := requireAdmin(a); e != nil {
		return nil, e
	}
	l, o := p.bounds()
	rows, e := s.db.QueryContext(ctx, "SELECT id,actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id FROM audit_events WHERE (?='' OR action=?) ORDER BY id DESC LIMIT ? OFFSET ?", p.Action, p.Action, l, o)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var x AuditEvent
		var ts int64
		if e = rows.Scan(&x.ID, &x.ActorID, &x.ActorType, &x.ActorName, &x.Action, &x.Target, &ts, &x.Outcome, &x.RequestID); e != nil {
			return nil, e
		}
		x.Timestamp = stamp(ts)
		out = append(out, x)
	}
	return out, rows.Err()
}
