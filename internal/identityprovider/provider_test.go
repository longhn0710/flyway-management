package identityprovider

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
	"golang.org/x/oauth2"
)

func TestLDAPConfigRequiresVerifiedSecureTransportAndStableMapping(t *testing.T) {
	base := LDAPConfig{URL: "ldap://directory.example:389", StartTLS: true, BaseDN: "dc=example,dc=com", BindDNTemplate: "uid={username},ou=people,dc=example,dc=com", UsernameAttribute: "uid", SubjectAttribute: "entryUUID"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid StartTLS config rejected: %v", err)
	}
	bad := base
	bad.StartTLS = false
	if bad.Validate() == nil {
		t.Fatal("plain LDAP accepted")
	}
	bad = base
	bad.URL = "ldaps://directory.example:636"
	bad.StartTLS = true
	if bad.Validate() == nil {
		t.Fatal("LDAPS and StartTLS combination accepted")
	}
	bad = base
	bad.SubjectAttribute = "uid)(|(objectClass=*)"
	if bad.Validate() == nil {
		t.Fatal("unsafe attribute accepted")
	}
	for _, raw := range []string{"ldaps://user:pass@directory.example:636", "ldaps://directory.example:636?x=y", "ldaps://directory.example:636#fragment", "ldaps://directory.example:636/dc=example"} {
		bad = base
		bad.URL = raw
		bad.StartTLS = false
		if bad.Validate() == nil {
			t.Errorf("unsafe LDAP endpoint accepted: %s", raw)
		}
	}
}

func TestLDAPRejectsUntrustedLDAPSCertificate(t *testing.T) {
	fixture := newLDAPFixture(t, true)
	defer fixture.close()
	if _, err := AuthenticateLDAP(context.Background(), fixture.config("ldaps", false, ""), "alice", "secret"); err == nil {
		t.Fatal("self-signed directory cert was trusted")
	}
}

func TestLDAPLDAPSBindAndStableSubjectSearch(t *testing.T) {
	fixture := newLDAPFixture(t, true)
	defer fixture.close()
	identity, err := AuthenticateLDAP(context.Background(), fixture.config("ldaps", false, fixture.caFile), "alice", "correct horse")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if identity.Subject != "immutable-9f" || identity.Username != "alice" || identity.DisplayName != "Alice Example" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
	if _, err := AuthenticateLDAP(context.Background(), fixture.config("ldaps", false, fixture.caFile), "alice", "wrong"); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("password failure was not safely reported: %v", err)
	}
}

func TestLDAPStartTLSBindAndRejectsTLSFailure(t *testing.T) {
	fixture := newLDAPFixture(t, false)
	defer fixture.close()
	identity, err := AuthenticateLDAP(context.Background(), fixture.config("ldap", true, fixture.caFile), "alice", "correct horse")
	if err != nil || identity.Subject != "immutable-9f" {
		t.Fatalf("StartTLS authenticate identity=%#v err=%v", identity, err)
	}
	if _, err := AuthenticateLDAP(context.Background(), fixture.config("ldap", true, ""), "alice", "correct horse"); err == nil {
		t.Fatal("untrusted StartTLS certificate was accepted")
	}
}

func TestOIDCAuthCodePKCEAndValidatedIDTokens(t *testing.T) {
	fixture := newOIDCFixture(t)
	defer fixture.server.Close()
	o, err := NewOIDC(context.Background(), fixture.config())
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	verifier := "0123456789abcdef0123456789abcdef0123456789abcdef"
	auth, err := o.AuthURL("state-123", "nonce-123", verifier)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(auth)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("state") != "state-123" || q.Get("nonce") != "nonce-123" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != oauth2.S256ChallengeFromVerifier(verifier) {
		t.Fatalf("bad authorization request: %s", auth)
	}
	identity, err := o.Exchange(context.Background(), "valid", verifier, "nonce-123")
	if err != nil {
		t.Fatalf("valid exchange: %v", err)
	}
	if identity.Issuer != fixture.server.URL || identity.Subject != "subject-abc" || identity.Username != "alice" || identity.DisplayName != "Alice Example" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
	for _, code := range []string{"wrong_nonce", "wrong_issuer", "wrong_audience", "expired", "bad_signature", "bad_code", "wrong_pkce"} {
		t.Run(code, func(t *testing.T) {
			useVerifier := verifier
			if code == "wrong_pkce" {
				useVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
			}
			_, err := o.Exchange(context.Background(), code, useVerifier, "nonce-123")
			if err == nil {
				t.Fatal("invalid exchange succeeded")
			}
			for _, secret := range []string{"correct horse", "client-secret", "signed-token", "private"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error exposed sensitive data: %v", err)
				}
			}
		})
	}
}

func TestOIDCRejectsInsecureDiscoveredEndpoints(t *testing.T) {
	for _, endpoint := range []string{"authorization_endpoint", "token_endpoint", "jwks_uri"} {
		t.Run(endpoint, func(t *testing.T) {
			fixture := newOIDCFixture(t)
			defer fixture.server.Close()
			fixture.discoveryOverrides = map[string]string{endpoint: "http://downgrade.example/endpoint"}
			if _, err := NewOIDC(context.Background(), fixture.config()); err == nil {
				t.Fatalf("OIDC accepted an HTTP %s", endpoint)
			}
		})
	}
}

func TestOIDCHTTPClientRejectsDowngradeAndCredentialRedirects(t *testing.T) {
	for _, location := range []string{"http://redirect.example/token", "https://user:secret@redirect.example/token"} {
		t.Run(location, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, location, http.StatusFound) }))
			defer server.Close()
			caFile := t.TempDir() + "/ca.pem"
			if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := providerHTTPClient(caFile)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Get(server.URL)
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil {
				t.Fatal("insecure redirect was followed")
			}
		})
	}
}

func TestProviderConfigRejectsUnsafeIssuerURLs(t *testing.T) {
	base := OIDCConfig{Issuer: "https://issuer.example", ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.example/callback"}
	for _, issuer := range []string{"http://issuer.example", "https://user:pass@issuer.example", "https://issuer.example/#fragment"} {
		bad := base
		bad.Issuer = issuer
		if bad.Validate() == nil {
			t.Errorf("unsafe issuer accepted: %q", issuer)
		}
	}
	for _, redirect := range []string{"http://app.example/callback", "https://user:pass@app.example/callback", "https://app.example/callback#fragment"} {
		bad := base
		bad.RedirectURL = redirect
		if bad.Validate() == nil {
			t.Errorf("unsafe redirect accepted: %q", redirect)
		}
	}
}

type oidcFixture struct {
	server             *httptest.Server
	signingKey         *rsa.PrivateKey
	badKey             *rsa.PrivateKey
	caFile             string
	discoveryOverrides map[string]string
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	badKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &oidcFixture{signingKey: key, badKey: badKey}
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			document := map[string]any{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/authorize", "token_endpoint": srv.URL + "/token", "jwks_uri": srv.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}}
			for key, value := range f.discoveryOverrides {
				document[key] = value
			}
			_ = json.NewEncoder(w).Encode(document)
		case "/keys":
			pub := f.signingKey.PublicKey
			n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(bigEndianInt(pub.E))
			_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"RSA","use":"sig","kid":"test-key","alg":"RS256","n":%q,"e":%q}]}`, n, e)
		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", 400)
				return
			}
			clientID, clientSecret, ok := r.BasicAuth()
			if !ok || clientID != "flyway" || clientSecret != "client-secret" {
				http.Error(w, "invalid client", 401)
				return
			}
			if r.Form.Get("code_verifier") != "0123456789abcdef0123456789abcdef0123456789abcdef" {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			switch r.Form.Get("code") {
			case "bad_code":
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			claims := map[string]any{"iss": srv.URL, "sub": "subject-abc", "aud": "flyway", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": "nonce-123", "preferred_username": "alice", "name": "Alice Example"}
			switch r.Form.Get("code") {
			case "wrong_nonce":
				claims["nonce"] = "other"
			case "wrong_issuer":
				claims["iss"] = "https://other.example"
			case "wrong_audience":
				claims["aud"] = "other-client"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			}
			key := f.signingKey
			if r.Form.Get("code") == "bad_signature" {
				key = f.badKey
			}
			jwt, err := signIDToken(key, claims)
			if err != nil {
				http.Error(w, "sign failure", 500)
				return
			}
			_, _ = fmt.Fprintf(w, `{"access_token":"opaque-access-token","token_type":"Bearer","expires_in":300,"id_token":%q}`, jwt)
		default:
			http.NotFound(w, r)
		}
	}))
	f.server = srv
	caPath := t.TempDir() + "/oidc-ca.pem"
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	f.caFile = caPath
	return f
}
func (f *oidcFixture) config() OIDCConfig {
	return OIDCConfig{Issuer: f.server.URL, ClientID: "flyway", ClientSecret: "client-secret", RedirectURL: "https://app.example/callback", CAFile: f.caFile}
}
func signIDToken(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	p, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
func bigEndianInt(v int) []byte {
	if v == 0 {
		return []byte{0}
	}
	var out []byte
	for v > 0 {
		out = append([]byte{byte(v)}, out...)
		v >>= 8
	}
	return out
}

type ldapFixture struct {
	listener  net.Listener
	tlsConfig *tls.Config
	caFile    string
}

func newLDAPFixture(t *testing.T, ldaps bool) *ldapFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	certPath := t.TempDir() + "/ldap-ca.pem"
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &ldapFixture{listener: listener, tlsConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, caFile: certPath}
	go f.serve(ldaps)
	return f
}
func (f *ldapFixture) serve(ldaps bool) {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			if ldaps {
				c = tls.Server(c, f.tlsConfig)
				if err := c.(*tls.Conn).Handshake(); err != nil {
					_ = c.Close()
					return
				}
			}
			f.handle(c, ldaps)
		}(conn)
	}
}
func (f *ldapFixture) handle(conn net.Conn, tlsActive bool) {
	defer conn.Close()
	for {
		packet, err := ber.ReadPacket(conn)
		if err != nil {
			return
		}
		if len(packet.Children) < 2 {
			return
		}
		id, ok := packet.Children[0].Value.(int64)
		if !ok {
			return
		}
		op := packet.Children[1]
		if op.ClassType == ber.ClassApplication && int(op.Tag) == ldap.ApplicationExtendedRequest {
			if len(op.Children) < 1 || op.Children[0].Data.String() != "1.3.6.1.4.1.1466.20037" {
				sendLDAPResult(conn, id, ldap.ApplicationExtendedResponse, protocolError)
				return
			}
			sendLDAPResult(conn, id, ldap.ApplicationExtendedResponse, 0)
			tlsConn := tls.Server(conn, f.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			tlsActive = true
			continue
		}
		if op.ClassType != ber.ClassApplication {
			return
		}
		switch int(op.Tag) {
		case ldap.ApplicationBindRequest:
			if len(op.Children) < 3 {
				return
			}
			dn := op.Children[1].Data.String()
			password := op.Children[2].Data.Bytes()
			if dn != "uid=alice,ou=people,dc=example,dc=com" || string(password) != "correct horse" {
				sendLDAPResult(conn, id, ldap.ApplicationBindResponse, 49)
			} else {
				sendLDAPResult(conn, id, ldap.ApplicationBindResponse, 0)
			}
		case ldap.ApplicationSearchRequest:
			if !tlsActive {
				return
			}
			entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationSearchResultEntry, nil, "Search Result Entry")
			entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "uid=alice,ou=people,dc=example,dc=com", "Object Name"))
			attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attributes")
			for name, value := range map[string]string{"uid": "alice", "entryUUID": "immutable-9f", "cn": "Alice Example"} {
				attr := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attribute")
				attr.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "Attribute Name"))
				values := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "Attribute Values")
				values.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, value, "Attribute Value"))
				attr.AppendChild(values)
				attrs.AppendChild(attr)
			}
			entry.AppendChild(attrs)
			sendLDAPOp(conn, id, entry)
			sendLDAPResult(conn, id, ldap.ApplicationSearchResultDone, 0)
		default:
			return
		}
	}
}

const protocolError = 2

func sendLDAPResult(conn net.Conn, id int64, tag int, code int) {
	op := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(tag), nil, "LDAP Result")
	op.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, int64(code), "Result Code"))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "Matched DN"))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "Diagnostic Message"))
	sendLDAPOp(conn, id, op)
}
func sendLDAPOp(conn net.Conn, id int64, op *ber.Packet) {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP Message")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "Message ID"))
	msg.AppendChild(op)
	_, _ = conn.Write(msg.Bytes())
}
func (f *ldapFixture) config(scheme string, start bool, ca string) LDAPConfig {
	host, port, _ := net.SplitHostPort(f.listener.Addr().String())
	return LDAPConfig{URL: fmt.Sprintf("%s://%s:%s", scheme, host, port), StartTLS: start, BaseDN: "dc=example,dc=com", BindDNTemplate: "uid={username},ou=people,dc=example,dc=com", UsernameAttribute: "uid", SubjectAttribute: "entryUUID", DisplayNameAttr: "cn", CAFile: ca}
}
func (f *ldapFixture) close() { _ = f.listener.Close() }
