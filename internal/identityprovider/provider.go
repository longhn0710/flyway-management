// Package identityprovider contains protocol adapters for externally authenticated identities.
// It deliberately does not create local users or assign application roles.
package identityprovider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-ldap/ldap/v3"
	"golang.org/x/oauth2"
)

// Identity identifies a principal at its external authority. Issuer and Subject form
// the durable identity key; Username and DisplayName are presentation attributes.
type Identity struct {
	Issuer      string
	Subject     string
	Username    string
	DisplayName string
}

// LDAPConfig uses a direct user bind, then reads immutable identity attributes as that user.
// URL must use ldaps://, or ldap:// with StartTLS enabled. BindDNTemplate receives {username}.
type LDAPConfig struct {
	URL               string
	StartTLS          bool
	BaseDN            string
	BindDNTemplate    string
	UsernameAttribute string
	SubjectAttribute  string
	DisplayNameAttr   string
	CAFile            string
}

func (c LDAPConfig) Validate() error {
	if strings.TrimSpace(c.URL) == "" || strings.TrimSpace(c.BaseDN) == "" || strings.TrimSpace(c.BindDNTemplate) == "" || strings.TrimSpace(c.UsernameAttribute) == "" || strings.TrimSpace(c.SubjectAttribute) == "" {
		return errors.New("LDAP URL, base DN, bind DN template, username attribute, and subject attribute are required")
	}
	if strings.Contains(c.BindDNTemplate, "{username}") == false {
		return errors.New("LDAP bind DN template must contain {username}")
	}
	if !validAttribute(c.UsernameAttribute) || !validAttribute(c.SubjectAttribute) || (c.DisplayNameAttr != "" && !validAttribute(c.DisplayNameAttr)) {
		return errors.New("invalid LDAP attribute name")
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("LDAP URL is invalid")
	}
	if strings.HasPrefix(strings.ToLower(c.URL), "ldaps://") {
		if c.StartTLS {
			return errors.New("StartTLS cannot be enabled for an ldaps URL")
		}
	} else if strings.HasPrefix(strings.ToLower(c.URL), "ldap://") {
		if !c.StartTLS {
			return errors.New("LDAP requires verified StartTLS")
		}
	} else {
		return errors.New("LDAP URL must use ldaps:// or ldap:// with StartTLS")
	}
	return nil
}

// Authenticate verifies credentials with an actual user bind and reads the stable subject
// only after that bind succeeds. Errors intentionally do not expose directory diagnostics.
func AuthenticateLDAP(ctx context.Context, c LDAPConfig, username, password string) (Identity, error) {
	if err := c.Validate(); err != nil {
		return Identity{}, err
	}
	if ctx == nil || ctx.Err() != nil {
		return Identity{}, errors.New("LDAP authentication failed")
	}
	if username == "" || password == "" {
		return Identity{}, errors.New("invalid credentials")
	}
	tlsConfig, err := tlsConfig(c.URL, c.CAFile)
	if err != nil {
		return Identity{}, errors.New("LDAP provider TLS configuration is invalid")
	}
	conn, err := ldap.DialURL(c.URL, ldap.DialWithTLSConfig(tlsConfig), ldap.DialWithDialer(&net.Dialer{Timeout: 8 * time.Second}))
	if err != nil {
		return Identity{}, errors.New("LDAP authentication failed")
	}
	defer conn.Close()
	conn.SetTimeout(8 * time.Second)
	if ctx == nil {
		return Identity{}, errors.New("LDAP authentication failed")
	}
	stopCancel := make(chan struct{})
	defer close(stopCancel)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopCancel:
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetTimeout(time.Until(deadline))
	}
	if strings.HasPrefix(strings.ToLower(c.URL), "ldap://") {
		if err := conn.StartTLS(tlsConfig); err != nil {
			return Identity{}, errors.New("LDAP authentication failed")
		}
	}
	dn := strings.ReplaceAll(c.BindDNTemplate, "{username}", ldap.EscapeDN(username))
	if err := conn.Bind(dn, password); err != nil {
		return Identity{}, errors.New("invalid credentials")
	}
	filter := "(" + c.UsernameAttribute + "=" + ldap.EscapeFilter(username) + ")"
	attrs := []string{c.SubjectAttribute, c.UsernameAttribute}
	if c.DisplayNameAttr != "" {
		attrs = append(attrs, c.DisplayNameAttr)
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, errors.New("LDAP authentication failed")
	}
	result, err := conn.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 8, false, filter, attrs, nil))
	if err != nil || len(result.Entries) != 1 {
		return Identity{}, errors.New("LDAP identity lookup failed")
	}
	entry := result.Entries[0]
	subject := stableLDAPSubject(entry, c.SubjectAttribute)
	if subject == "" {
		return Identity{}, errors.New("LDAP identity lookup failed")
	}
	name := entry.GetAttributeValue(c.DisplayNameAttr)
	if name == "" {
		name = entry.GetAttributeValue(c.UsernameAttribute)
	}
	return Identity{Issuer: strings.TrimSuffix(c.URL, "/"), Subject: subject, Username: entry.GetAttributeValue(c.UsernameAttribute), DisplayName: name}, nil
}

// OIDCConfig describes a public discovery issuer and confidential web client.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	CAFile       string
}

func (c OIDCConfig) Validate() error {
	if c.Issuer == "" || c.ClientID == "" || c.ClientSecret == "" || c.RedirectURL == "" {
		return errors.New("OIDC issuer, client ID, client secret, and redirect URL are required")
	}
	issuer, err := url.Parse(c.Issuer)
	redirect, redErr := url.Parse(c.RedirectURL)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("OIDC issuer must be an HTTPS URL")
	}
	if redErr != nil || redirect.Host == "" || redirect.User != nil || redirect.Fragment != "" || (redirect.Scheme != "https" && !(redirect.Scheme == "http" && (redirect.Hostname() == "localhost" || redirect.Hostname() == "127.0.0.1" || redirect.Hostname() == "::1"))) {
		return errors.New("OIDC redirect URL must use HTTPS, except for loopback development")
	}
	return nil
}

type OIDC struct {
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	oauth      oauth2.Config
	issuer     string
	httpClient *http.Client
}

func NewOIDC(ctx context.Context, c OIDCConfig) (*OIDC, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("OIDC context is required")
	}
	httpClient, err := providerHTTPClient(c.CAFile)
	if err != nil {
		return nil, errors.New("OIDC TLS configuration is invalid")
	}
	ctx = oidc.ClientContext(ctx, httpClient)
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery failed: %w", err)
	}
	var metadata struct {
		AuthURL       string `json:"authorization_endpoint"`
		TokenURL      string `json:"token_endpoint"`
		JWKSURL       string `json:"jwks_uri"`
		DeviceAuthURL string `json:"device_authorization_endpoint"`
		UserInfoURL   string `json:"userinfo_endpoint"`
	}
	if err := provider.Claims(&metadata); err != nil || !safeProviderEndpoint(metadata.AuthURL) || !safeProviderEndpoint(metadata.TokenURL) || !safeProviderEndpoint(metadata.JWKSURL) || (metadata.DeviceAuthURL != "" && !safeProviderEndpoint(metadata.DeviceAuthURL)) || (metadata.UserInfoURL != "" && !safeProviderEndpoint(metadata.UserInfoURL)) {
		return nil, errors.New("OIDC discovery contains an invalid or insecure endpoint")
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: c.ClientID})
	return &OIDC{provider: provider, verifier: verifier, issuer: c.Issuer, httpClient: httpClient, oauth: oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: c.RedirectURL, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}}, nil
}

// AuthURL builds an authorization-code URL with PKCE S256. The caller must generate,
// persist and later verify unpredictable state and nonce values for the browser session.
func (o *OIDC) AuthURL(state, nonce, verifier string) (string, error) {
	if o == nil || o.provider == nil || state == "" || nonce == "" || !validPKCEVerifier(verifier) {
		return "", errors.New("OIDC state, nonce, and PKCE verifier are required")
	}
	challenge := oauth2.S256ChallengeFromVerifier(verifier)
	return o.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", challenge), oauth2.SetAuthURLParam("code_challenge_method", "S256")), nil
}

// Exchange redeems a code using the original PKCE verifier, verifies signature, issuer,
// audience and expiry via go-oidc, then checks the caller's nonce against the ID token.
func (o *OIDC) Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error) {
	if o == nil || o.verifier == nil || ctx == nil || code == "" || !validPKCEVerifier(verifier) || nonce == "" {
		return Identity{}, errors.New("OIDC callback validation failed")
	}
	ctx = oidc.ClientContext(ctx, o.httpClient)
	token, err := o.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, errors.New("OIDC callback validation failed")
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, errors.New("OIDC callback validation failed")
	}
	idToken, err := o.verifier.Verify(ctx, raw)
	if err != nil || idToken.Nonce != nonce {
		return Identity{}, errors.New("OIDC callback validation failed")
	}
	var claims struct {
		Subject           string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
		Email             string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Subject == "" {
		return Identity{}, errors.New("OIDC callback validation failed")
	}
	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	return Identity{Issuer: o.issuer, Subject: claims.Subject, Username: username, DisplayName: claims.Name}, nil
}

func tlsConfig(endpoint, caFile string) (*tls.Config, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	pool, err := systemRoots(caFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname(), RootCAs: pool}, nil
}
func providerHTTPClient(caFile string) (*http.Client, error) {
	pool, err := systemRoots(caFile)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("OIDC provider redirect limit exceeded")
		}
		if req.URL.Scheme != "https" || req.URL.Hostname() == "" || req.URL.User != nil || req.URL.Fragment != "" {
			return errors.New("OIDC redirect must remain on verified HTTPS")
		}
		if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[len(via)-1].URL.Host) {
			return errors.New("OIDC redirect cannot change host")
		}
		return nil
	}}, nil
}
func systemRoots(caFile string) (*x509.CertPool, error) {
	if caFile == "" {
		return x509.SystemCertPool()
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	b, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	if !pool.AppendCertsFromPEM(b) {
		return nil, errors.New("CA file contains no certificates")
	}
	return pool, nil
}

var attributeName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

func validAttribute(s string) bool { return attributeName.MatchString(s) }

// Binary directory identifiers such as Active Directory objectGUID are returned in
// stable URL-safe base64 form; textual UUIDs and printable subjects remain unchanged.
func stableLDAPSubject(entry *ldap.Entry, attribute string) string {
	raw := entry.GetRawAttributeValue(attribute)
	if len(raw) == 0 {
		return ""
	}
	if utf8.Valid(raw) {
		printable := true
		for _, r := range string(raw) {
			if !unicode.IsPrint(r) {
				printable = false
				break
			}
		}
		if printable {
			return string(raw)
		}
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func validPKCEVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("-._~", r)) {
			return false
		}
	}
	return true
}

func safeProviderEndpoint(raw string) bool {
	endpoint, err := url.Parse(raw)
	return err == nil && endpoint.Scheme == "https" && endpoint.Hostname() != "" && endpoint.User == nil && endpoint.Fragment == "" && endpoint.Opaque == ""
}
