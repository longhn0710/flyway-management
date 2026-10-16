package core

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"

	"flyway-manager/internal/identityprovider"
)

type UserInput struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Provider    string `json:"provider"`
	Issuer      string `json:"issuer"`
	Subject     string `json:"subject"`
	Role        string `json:"role"`
}
type UserUpdate struct {
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Active      bool   `json:"active"`
}

func normalizeUser(in *UserInput) error {
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.Provider == "" {
		in.Provider = "local"
	}
	if in.Role == "" {
		in.Role = "Viewer"
	}
	if in.DisplayName == "" {
		in.DisplayName = in.Username
	}
	if !validText(in.Username, 128) || !validText(in.DisplayName, 200) || !validRole(in.Role) {
		return invalid("user", "Invalid username, display name or role.")
	}
	if in.Provider == "local" {
		if !identifier.MatchString(in.Username) {
			return invalid("username", "Username must use letters, digits, dot, dash or underscore.")
		}
		if err := validatePassword(in.Password); err != nil {
			return err
		}
		in.Issuer = ""
		in.Subject = in.Username
	} else if (in.Provider != "ldap" && in.Provider != "oidc") || !validText(in.Issuer, 512) || !validText(in.Subject, 512) || in.Password != "" {
		return invalid("provider", "External identities require an explicit provider, issuer and immutable subject, without a password.")
	}
	return nil
}
func insertUser(ctx context.Context, tx *sql.Tx, u UserInput, uid, hash string) error {
	var password any
	if u.Provider == "local" {
		password = hash
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO users(id,username,display_name,provider,issuer,subject,password_hash,role,active,created_at) VALUES(?,?,?,?,?,?,?,?,1,?)", uid, u.Username, u.DisplayName, u.Provider, u.Issuer, u.Subject, password, u.Role, now())
	return err
}
func (s *Service) Bootstrap(ctx context.Context, t Trace, in UserInput, setupToken string) (SessionResult, string, error) {
	if s.setupHash == "" || subtle.ConstantTimeCompare([]byte(digest(setupToken)), []byte(s.setupHash)) != 1 {
		return SessionResult{}, "", denied()
	}
	in.Provider = "local"
	in.Role = "Admin"
	if err := normalizeUser(&in); err != nil {
		return SessionResult{}, "", err
	}
	hash, err := s.hashPassword(ctx, in.Password)
	if err != nil {
		return SessionResult{}, "", err
	}
	uid := id()
	t.Actor = Actor{ID: uid, Name: in.Username, Kind: "user", Role: "Admin"}
	result, raw, err := s.createSession(ctx, t, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM installation_metadata WHERE key='admin_bootstrapped'").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return conflict("Administrator setup has already completed.")
		}
		if err := insertUser(ctx, tx, in, uid, hash); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO installation_metadata(key,value) VALUES('admin_bootstrapped','true')")
		return err
	}, "auth.bootstrap")
	return result, raw, err
}
func (s *Service) CreateUser(ctx context.Context, t Trace, in UserInput) (User, error) {
	if err := requireAdmin(t.Actor); err != nil {
		return User{}, err
	}
	if err := normalizeUser(&in); err != nil {
		return User{}, err
	}
	var hash string
	var err error
	if in.Provider == "local" {
		hash, err = s.hashPassword(ctx, in.Password)
		if err != nil {
			return User{}, err
		}
	}
	uid := id()
	err = s.mutation(ctx, t, "user.create", uid, func(tx *sql.Tx) error { return insertUser(ctx, tx, in, uid, hash) })
	if err != nil {
		return User{}, err
	}
	return s.User(ctx, uid)
}
func (s *Service) Users(ctx context.Context, a Actor, p Page) ([]User, error) {
	if err := requireAdmin(a); err != nil {
		return nil, err
	}
	l, o := p.bounds()
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users WHERE username LIKE ? OR display_name LIKE ? ORDER BY username,id LIMIT ? OFFSET ?", "%"+p.Query+"%", "%"+p.Query+"%", l, o)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, e := userScan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Service) UpdateUser(ctx context.Context, t Trace, uid string, in UserUpdate) (User, error) {
	if err := requireAdmin(t.Actor); err != nil {
		return User{}, err
	}
	if !validText(in.DisplayName, 200) || !validRole(in.Role) {
		return User{}, invalid("user", "Invalid display name or role.")
	}
	err := s.mutation(ctx, t, "user.update", uid, func(tx *sql.Tx) error {
		old, e := userScan(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id=?", uid))
		if e != nil {
			return e
		}
		if old.Active && old.Role == "Admin" && old.Provider == "local" && (!in.Active || in.Role != "Admin") {
			var n int
			if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE active=1 AND role='Admin' AND provider='local' AND id<>?", uid).Scan(&n); e != nil {
				return e
			}
			if n == 0 {
				return conflict("The final active local administrator must be preserved.")
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE users SET display_name=?,role=?,active=? WHERE id=?", strings.TrimSpace(in.DisplayName), in.Role, in.Active, uid); e != nil {
			return e
		}
		cfg, e := s.settingsFrom(ctx, tx)
		if e != nil {
			return e
		}
		var reachable int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE active=1 AND role='Admin' AND ((provider='local' AND ?=1) OR (provider='ldap' AND issuer=? AND ?=1) OR (provider='oidc' AND issuer=? AND ?=1))`, cfg.LocalEnabled, cfg.LDAP.URL, cfg.LDAP.Enabled, cfg.OIDC.Issuer, cfg.OIDC.Enabled).Scan(&reachable); e != nil {
			return e
		}
		if reachable == 0 {
			return conflict("Preserve an active administrator with an enabled authentication provider.")
		}
		if !in.Active {
			if _, e = tx.ExecContext(ctx, "UPDATE api_tokens SET revoked=1 WHERE service_id IN (SELECT id FROM service_accounts WHERE owner_id=?)", uid); e != nil {
				return e
			}
			if e = audit(ctx, tx, t, "token.revoke_owner", uid, "success"); e != nil {
				return e
			}
		}
		if !in.Active || in.Role != old.Role {
			_, e = tx.ExecContext(ctx, "UPDATE sessions SET revoked=1 WHERE user_id=?", uid)
		}
		if e != nil {
			return e
		}
		if in.Role != old.Role {
			return audit(ctx, tx, t, "permissions.role_update", uid, "success")
		}
		return e
	})
	if err != nil {
		return User{}, err
	}
	return s.User(ctx, uid)
}

type LoginInput struct {
	Provider string `json:"provider"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Service) Login(ctx context.Context, t Trace, in LoginInput) (SessionResult, string, error) {
	in.Username = strings.TrimSpace(in.Username)
	if in.Provider == "local" {
		in.Username = strings.ToLower(in.Username)
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return SessionResult{}, "", err
	}
	invalidLogin := func() (SessionResult, string, error) {
		if e := s.Event(ctx, t, "auth.login", "login", "failure"); e != nil {
			return SessionResult{}, "", e
		}
		return SessionResult{}, "", &Error{401, "INVALID_CREDENTIALS", "Invalid credentials or unavailable account.", ""}
	}
	if !validText(in.Username, 128) || len(in.Password) > 1024 || in.Password == "" {
		return invalidLogin()
	}
	var u User
	if in.Provider == "local" && settings.LocalEnabled {
		var hash string
		e := s.db.QueryRowContext(ctx, "SELECT id,password_hash FROM users WHERE provider='local' AND username=? AND active=1", in.Username).Scan(&u.ID, &hash)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return SessionResult{}, "", e
		}
		if e != nil {
			hash = s.dummyHash
		}
		matches := s.passwordMatches(ctx, in.Password, hash)
		if e != nil || !matches {
			return invalidLogin()
		}
		u, err = s.User(ctx, u.ID)
	} else if in.Provider == "ldap" && settings.LDAP.Enabled {
		identity, e := identityprovider.AuthenticateLDAP(ctx, settings.LDAP.Protocol(), in.Username, in.Password)
		if e != nil {
			return invalidLogin()
		}
		u, err = userScan(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE provider='ldap' AND issuer=? AND subject=? AND active=1", identity.Issuer, identity.Subject))
		if err != nil {
			return invalidLogin()
		}
	} else {
		return invalidLogin()
	}
	if err != nil {
		return SessionResult{}, "", err
	}
	t.Actor = Actor{ID: u.ID, Name: u.Username, Kind: "user", Role: u.Role}
	return s.createSession(ctx, t, nil, "auth.login")
}

type SessionResult struct {
	User      User    `json:"user"`
	CSRFToken string  `json:"csrf_token"`
	ExpiresAt string  `json:"expires_at"`
	Grants    []Grant `json:"grants"`
}

func (s *Service) createSession(ctx context.Context, t Trace, before func(*sql.Tx) error, action string) (SessionResult, string, error) {
	secret := random()
	csrf := random()
	sid := id()
	expiry := now() + 12*3600
	err := s.mutation(ctx, t, action, t.Actor.ID, func(tx *sql.Tx) error {
		if before != nil {
			if err := before(tx); err != nil {
				return err
			}
		}
		// Keep the session table bounded per user and discard stale records.
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<? OR (user_id=? AND revoked=1)", now(), t.Actor.ID); err != nil {
			return err
		}
		var enabled bool
		if err := tx.QueryRowContext(ctx, "SELECT active FROM users WHERE id=?", t.Actor.ID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return unauthorized()
		}
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET revoked=1 WHERE id IN (SELECT id FROM sessions WHERE user_id=? AND revoked=0 ORDER BY created_at DESC LIMIT -1 OFFSET 19)", t.Actor.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO sessions(id,user_id,secret_hash,csrf_token,created_at,expires_at,last_used_at) VALUES(?,?,?,?,?,?,?)", sid, t.Actor.ID, digest(secret), csrf, now(), expiry, now())
		return err
	})
	if err != nil {
		return SessionResult{}, "", err
	}
	u, err := s.User(ctx, t.Actor.ID)
	if err != nil {
		return SessionResult{}, "", err
	}
	gs, err := s.Grants(ctx, "user", u.ID)
	return SessionResult{u, csrf, stamp(expiry), gs}, secret, err
}
func (s *Service) AuthenticateSession(ctx context.Context, secret string) (Actor, error) {
	if len(secret) != 43 {
		return Actor{}, unauthorized()
	}
	var a Actor
	var active, revoked bool
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.role,u.active,s.id,s.csrf_token,s.expires_at,s.last_used_at,s.revoked FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.secret_hash=?`, digest(secret)).Scan(&a.ID, &a.Name, &a.Role, &active, &a.SessionID, &a.CSRF, &a.ExpiresAt, &last, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, unauthorized()
	}
	if err != nil {
		return Actor{}, err
	}
	if !active || revoked || a.ExpiresAt <= now() || last+30*60 <= now() {
		return Actor{}, unauthorized()
	}
	a.Kind = "user"
	if _, err = s.db.ExecContext(ctx, "UPDATE sessions SET last_used_at=? WHERE id=?", now(), a.SessionID); err != nil {
		return Actor{}, err
	}
	return a, nil
}
func (s *Service) Me(ctx context.Context, a Actor) (SessionResult, error) {
	if a.Kind != "user" {
		return SessionResult{}, denied()
	}
	u, e := s.User(ctx, a.ID)
	if e != nil {
		return SessionResult{}, e
	}
	gs, e := s.Grants(ctx, a.Kind, a.ID)
	return SessionResult{u, a.CSRF, stamp(a.ExpiresAt), gs}, e
}
func (s *Service) Logout(ctx context.Context, t Trace) error {
	return s.mutation(ctx, t, "auth.logout", t.Actor.SessionID, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE sessions SET revoked=1 WHERE id=? AND user_id=?", t.Actor.SessionID, t.Actor.ID)
		return e
	})
}

type SessionInfo struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	Current   bool   `json:"current"`
}

func (s *Service) Sessions(ctx context.Context, a Actor) ([]SessionInfo, error) {
	if a.Kind != "user" {
		return nil, denied()
	}
	rows, e := s.db.QueryContext(ctx, "SELECT id,created_at,expires_at FROM sessions WHERE user_id=? AND revoked=0 AND expires_at>? AND last_used_at>? ORDER BY created_at DESC", a.ID, now(), now()-30*60)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var x SessionInfo
		var c, ex int64
		if e = rows.Scan(&x.ID, &c, &ex); e != nil {
			return nil, e
		}
		x.CreatedAt = stamp(c)
		x.ExpiresAt = stamp(ex)
		x.Current = x.ID == a.SessionID
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) RevokeSession(ctx context.Context, t Trace, sid string) error {
	if t.Actor.Kind != "user" {
		return denied()
	}
	return s.mutation(ctx, t, "session.revoke", sid, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, "UPDATE sessions SET revoked=1 WHERE id=? AND user_id=?", sid, t.Actor.ID)
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
func (s *Service) ChangePassword(ctx context.Context, t Trace, current, replacement string) error {
	if t.Actor.Kind != "user" {
		return denied()
	}
	if len(current) > 1024 {
		return invalid("current_password", "Current password is too long.")
	}
	if err := validatePassword(replacement); err != nil {
		return err
	}
	var old string
	if err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=? AND provider='local'", t.Actor.ID).Scan(&old); err != nil {
		return denied()
	}
	if !s.passwordMatches(ctx, current, old) {
		return &Error{401, "INVALID_CREDENTIALS", "Current password is invalid.", ""}
	}
	hash, e := s.hashPassword(ctx, replacement)
	if e != nil {
		return e
	}
	return s.mutation(ctx, t, "password.change", t.Actor.ID, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=? AND password_hash=?", hash, t.Actor.ID, old)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return conflict("Password was changed concurrently.")
		}
		_, e = tx.ExecContext(ctx, "UPDATE sessions SET revoked=1 WHERE user_id=?", t.Actor.ID)
		return e
	})
}
