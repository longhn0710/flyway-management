package core

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"

	"flyway-manager/internal/identityprovider"
)

type LDAPSettings struct {
	Enabled              bool   `json:"enabled"`
	URL                  string `json:"url"`
	StartTLS             bool   `json:"start_tls"`
	BaseDN               string `json:"base_dn"`
	BindDNTemplate       string `json:"bind_dn_template"`
	SubjectAttribute     string `json:"subject_attribute"`
	CAFile               string `json:"ca_file"`
	UsernameAttribute    string `json:"username_attribute"`
	DisplayNameAttribute string `json:"display_name_attribute"`
}

func (c LDAPSettings) Protocol() identityprovider.LDAPConfig {
	username := c.UsernameAttribute
	if username == "" {
		username = "uid"
	}
	display := c.DisplayNameAttribute
	if display == "" {
		display = "displayName"
	}
	return identityprovider.LDAPConfig{URL: c.URL, StartTLS: c.StartTLS, BaseDN: c.BaseDN, BindDNTemplate: c.BindDNTemplate, SubjectAttribute: c.SubjectAttribute, UsernameAttribute: username, DisplayNameAttr: display, CAFile: c.CAFile}
}

type OIDCSettings struct {
	Enabled                bool   `json:"enabled"`
	Issuer                 string `json:"issuer"`
	ClientID               string `json:"client_id"`
	RedirectURL            string `json:"redirect_url"`
	CAFile                 string `json:"ca_file"`
	ClientSecret           string `json:"client_secret,omitempty"`
	ClientSecretConfigured bool   `json:"client_secret_configured"`
}

func (c OIDCSettings) Protocol() identityprovider.OIDCConfig {
	return identityprovider.OIDCConfig{Issuer: c.Issuer, ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: c.RedirectURL, CAFile: c.CAFile}
}

type AuthSettings struct {
	LocalEnabled bool         `json:"local_enabled"`
	LDAP         LDAPSettings `json:"ldap"`
	OIDC         OIDCSettings `json:"oidc"`
}

func (s *Service) Settings(ctx context.Context) (AuthSettings, error) {
	return s.settingsFrom(ctx, s.db)
}
func (s *Service) settingsFrom(ctx context.Context, q queryer) (AuthSettings, error) {
	cfg := AuthSettings{LocalEnabled: true, LDAP: LDAPSettings{SubjectAttribute: "entryUUID", UsernameAttribute: "uid", DisplayNameAttribute: "displayName"}}
	var payload []byte
	err := q.QueryRowContext(ctx, "SELECT payload FROM auth_settings WHERE id=1").Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	err = s.openJSON("auth-settings", payload, &cfg)
	return cfg, err
}
func (s *Service) PublicSettings(ctx context.Context, a Actor) (AuthSettings, error) {
	if err := requireAdmin(a); err != nil {
		return AuthSettings{}, err
	}
	c, e := s.Settings(ctx)
	c.OIDC.ClientSecretConfigured = c.OIDC.ClientSecret != ""
	c.OIDC.ClientSecret = ""
	return c, e
}
func (s *Service) UpdateSettings(ctx context.Context, t Trace, c AuthSettings) (AuthSettings, error) {
	if err := requireAdmin(t.Actor); err != nil {
		return AuthSettings{}, err
	}
	old, err := s.Settings(ctx)
	if err != nil {
		return AuthSettings{}, err
	}
	if c.OIDC.ClientSecret == "" {
		c.OIDC.ClientSecret = old.OIDC.ClientSecret
	}
	c.OIDC.ClientSecretConfigured = false
	if len(c.OIDC.ClientSecret) > 4096 {
		return AuthSettings{}, invalid("oidc", "Client secret is too long.")
	}
	if c.LDAP.Enabled {
		if err := c.LDAP.Protocol().Validate(); err != nil {
			return AuthSettings{}, invalid("ldap", err.Error())
		}
	}
	if c.OIDC.Enabled {
		if err := c.OIDC.Protocol().Validate(); err != nil {
			return AuthSettings{}, invalid("oidc", err.Error())
		}
		if s.PublicURL != "" && c.OIDC.RedirectURL != strings.TrimRight(s.PublicURL, "/")+"/api/v1/auth/oidc/callback" {
			return AuthSettings{}, invalid("redirect_url", "OIDC callback must match the configured public URL and /api/v1/auth/oidc/callback.")
		}
	}
	payload, err := s.sealJSON("auth-settings", c)
	if err != nil {
		return AuthSettings{}, err
	}
	err = s.mutation(ctx, t, "authentication.update", "providers", func(tx *sql.Tx) error {
		current, e := s.settingsFrom(ctx, tx)
		if e != nil {
			return e
		}
		if current != old {
			return conflict("Authentication settings changed concurrently. Reload before saving.")
		}
		if !c.LocalEnabled {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role='Admin' AND active=1 AND ((provider='ldap' AND issuer=? AND ?=1) OR (provider='oidc' AND issuer=? AND ?=1))`, c.LDAP.URL, c.LDAP.Enabled, c.OIDC.Issuer, c.OIDC.Enabled).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return conflict("Keep local login enabled until an active external administrator is explicitly registered with an enabled provider.")
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO auth_settings(id,payload) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", payload); err != nil {
			return err
		}
		// Configuration changes invalidate outstanding login challenges.
		_, err := tx.ExecContext(ctx, "DELETE FROM oidc_flows")
		return err
	})
	if err != nil {
		return AuthSettings{}, err
	}
	return s.PublicSettings(ctx, t.Actor)
}
func (s *Service) Providers(ctx context.Context) ([]string, error) {
	c, e := s.Settings(ctx)
	if e != nil {
		return nil, e
	}
	out := []string{}
	if c.LocalEnabled {
		out = append(out, "local")
	}
	if c.LDAP.Enabled {
		out = append(out, "ldap")
	}
	if c.OIDC.Enabled {
		out = append(out, "oidc")
	}
	return out, nil
}

type oidcFlow struct{ Verifier, Nonce, CookieHash, ConfigHash string }

func configHash(c OIDCSettings) string {
	return digest(c.Issuer + "\x00" + c.ClientID + "\x00" + c.RedirectURL + "\x00" + c.ClientSecret + "\x00" + c.CAFile)
}
func (s *Service) OIDCStart(ctx context.Context) (location, cookie string, err error) {
	cfg, err := s.Settings(ctx)
	if err != nil {
		return "", "", err
	}
	if !cfg.OIDC.Enabled {
		return "", "", &Error{503, "PROVIDER_UNAVAILABLE", "OIDC login is unavailable.", ""}
	}
	client, err := identityprovider.NewOIDC(ctx, cfg.OIDC.Protocol())
	if err != nil {
		return "", "", &Error{503, "PROVIDER_UNAVAILABLE", "OIDC provider is unavailable.", ""}
	}
	state, cookie := random(), random()
	flow := oidcFlow{random(), random(), digest(cookie), configHash(cfg.OIDC)}
	location, err = client.AuthURL(state, flow.Nonce, flow.Verifier)
	if err != nil {
		return "", "", err
	}
	payload, err := s.sealJSON("oidc:"+digest(state), flow)
	if err != nil {
		return "", "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM oidc_flows WHERE expires_at<=?", now()); err != nil {
		return "", "", err
	}
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM oidc_flows").Scan(&n); err != nil {
		return "", "", err
	}
	if n >= 1000 {
		return "", "", &Error{429, "RATE_LIMITED", "Too many pending login attempts.", ""}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO oidc_flows(state_hash,payload,expires_at) VALUES(?,?,?)", digest(state), payload, now()+5*60); err != nil {
		return "", "", err
	}
	err = tx.Commit()
	return location, cookie, err
}
func (s *Service) OIDCCallback(ctx context.Context, t Trace, state, cookie, code string) (SessionResult, string, error) {
	failure := func() (SessionResult, string, error) {
		if e := s.Event(ctx, t, "auth.oidc", "oidc", "failure"); e != nil {
			return SessionResult{}, "", e
		}
		return SessionResult{}, "", &Error{401, "INVALID_CREDENTIALS", "SSO login could not be verified.", ""}
	}
	if len(state) != 43 || len(cookie) != 43 || len(code) == 0 || len(code) > 4096 {
		return failure()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionResult{}, "", err
	}
	defer tx.Rollback()
	var payload []byte
	var expiry int64
	if err = tx.QueryRowContext(ctx, "SELECT payload,expires_at FROM oidc_flows WHERE state_hash=?", digest(state)).Scan(&payload, &expiry); err != nil {
		tx.Rollback()
		return failure()
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM oidc_flows WHERE state_hash=?", digest(state)); err != nil {
		return SessionResult{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return SessionResult{}, "", err
	}
	var flow oidcFlow
	if err = s.openJSON("oidc:"+digest(state), payload, &flow); err != nil {
		return failure()
	}
	if expiry <= now() || subtle.ConstantTimeCompare([]byte(flow.CookieHash), []byte(digest(cookie))) != 1 {
		return failure()
	}
	cfg, err := s.Settings(ctx)
	if err != nil {
		return SessionResult{}, "", err
	}
	if !cfg.OIDC.Enabled || flow.ConfigHash != configHash(cfg.OIDC) {
		return failure()
	}
	client, err := identityprovider.NewOIDC(ctx, cfg.OIDC.Protocol())
	if err != nil {
		return failure()
	}
	identity, err := client.Exchange(ctx, code, flow.Verifier, flow.Nonce)
	if err != nil {
		return failure()
	}
	u, err := userScan(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE provider='oidc' AND issuer=? AND subject=? AND active=1", identity.Issuer, identity.Subject))
	if err != nil {
		return failure()
	}
	t.Actor = Actor{ID: u.ID, Name: u.Username, Kind: "user", Role: u.Role}
	return s.createSession(ctx, t, nil, "auth.oidc")
}
