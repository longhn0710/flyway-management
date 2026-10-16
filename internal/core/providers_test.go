package core

import (
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

func TestOIDCSettingsCallbackSessionAndReplay(t *testing.T) {
	f := newFixture(t)
	idp := newCoreOIDCFixture(t)
	defer idp.server.Close()
	settings := AuthSettings{LocalEnabled: true, OIDC: OIDCSettings{Enabled: true, Issuer: idp.server.URL, ClientID: "flyway-test", ClientSecret: "oidc-client-secret", RedirectURL: "https://manager.example/api/v1/auth/oidc/callback", CAFile: idp.caFile}}
	_, err := f.s.UpdateSettings(testCtx, f.trace(), settings)
	must(t, err)
	issuer := idp.server.URL
	user, err := f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "oidc-alice", DisplayName: "Alice", Provider: "oidc", Issuer: issuer, Subject: "oidc-subject-alice", Role: "Viewer"})
	must(t, err)
	if user.Role != "Viewer" {
		t.Fatalf("external user received unexpected role %q", user.Role)
	}

	location, cookie, err := f.s.OIDCStart(testCtx)
	must(t, err)
	u, err := url.Parse(location)
	must(t, err)
	state := u.Query().Get("state")
	idp.challenge = u.Query().Get("code_challenge")
	idp.nonce = u.Query().Get("nonce")
	if state == "" || u.Query().Get("nonce") == "" || u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("code_challenge") == "" {
		t.Fatalf("OIDC start omitted state, nonce or expected PKCE challenge: %s", location)
	}
	session, raw, err := f.s.OIDCCallback(testCtx, Trace{RequestID: "oidc-login"}, state, cookie, "good")
	must(t, err)
	if session.User.ID != user.ID || session.User.Role != "Viewer" {
		t.Fatalf("OIDC identity mismatch or role escalation: %#v", session.User)
	}
	actor, err := f.s.AuthenticateSession(testCtx, raw)
	must(t, err)
	if actor.ID != user.ID || actor.Role != "Viewer" {
		t.Fatalf("session actor mismatch: %#v", actor)
	}
	_, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "good")
	status(t, err, 401)
	var active int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM sessions WHERE user_id=? AND revoked=0", user.ID).Scan(&active))
	if active != 1 {
		t.Fatalf("replay created another session: %d", active)
	}
}

func TestOIDCCallbackRejectsStateCookieNonceAndUnprovisionedSubject(t *testing.T) {
	f := newFixture(t)
	idp := newCoreOIDCFixture(t)
	defer idp.server.Close()
	_, err := f.s.UpdateSettings(testCtx, f.trace(), AuthSettings{LocalEnabled: true, OIDC: OIDCSettings{Enabled: true, Issuer: idp.server.URL, ClientID: "flyway-test", ClientSecret: "oidc-client-secret", RedirectURL: "https://manager.example/api/v1/auth/oidc/callback", CAFile: idp.caFile}})
	must(t, err)
	_, err = f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "oidc-existing", Provider: "oidc", Issuer: idp.server.URL, Subject: "oidc-subject-existing", Role: "Operator"})
	must(t, err)
	var before int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM users").Scan(&before))

	start := func() (string, string) {
		location, cookie, e := f.s.OIDCStart(testCtx)
		must(t, e)
		p, e := url.Parse(location)
		must(t, e)
		idp.challenge = p.Query().Get("code_challenge")
		idp.nonce = p.Query().Get("nonce")
		return p.Query().Get("state"), cookie
	}
	state, cookie := start()
	_, _, err = f.s.OIDCCallback(testCtx, Trace{}, random(), cookie, "good")
	status(t, err, 401)
	// A wrong state does not consume the actual browser flow.
	_, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, random(), "good")
	status(t, err, 401)
	_, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "wrong_nonce")
	status(t, err, 401)
	// Flow was consumed even though nonce validation failed.
	_, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "good")
	status(t, err, 401)
	state, cookie = start()
	_, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "missing_subject")
	status(t, err, 401)
	var after int
	must(t, f.db.SQL.QueryRow("SELECT count(*) FROM users").Scan(&after))
	if before != after {
		t.Fatalf("unprovisioned/invalid subject created an account: before=%d after=%d", before, after)
	}
	state, cookie = start()
	_, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "unregistered")
	status(t, err, 401) // valid but unregistered subject
	disabled, err := f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "disabled-oidc", Provider: "oidc", Issuer: idp.server.URL, Subject: "oidc-subject-alice", Role: "Viewer"})
	must(t, err)
	_, err = f.s.UpdateUser(testCtx, f.trace(), disabled.ID, UserUpdate{DisplayName: "Disabled", Role: "Viewer", Active: false})
	must(t, err)
	state, cookie = start()
	_, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "good")
	status(t, err, 401)
}

func TestLDAPSettingsDirectBindMappingAndNoLocalFallback(t *testing.T) {
	f := newFixture(t)
	idp := newCoreLDAPFixture(t, true)
	defer idp.close()
	endpoint := idp.url("ldaps")
	_, err := f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "alice", Password: testPassword, Role: "Viewer"})
	must(t, err)
	_, err = f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "directory-alice", DisplayName: "Alice Directory", Provider: "ldap", Issuer: endpoint, Subject: "stable-directory-alice", Role: "Operator"})
	must(t, err)
	_, err = f.s.UpdateSettings(testCtx, f.trace(), AuthSettings{LocalEnabled: true, LDAP: LDAPSettings{Enabled: true, URL: endpoint, BaseDN: "dc=example,dc=com", BindDNTemplate: "uid={username},ou=people,dc=example,dc=com", UsernameAttribute: "uid", SubjectAttribute: "entryUUID", DisplayNameAttribute: "cn", CAFile: idp.caFile}})
	must(t, err)
	result, raw, err := f.s.Login(testCtx, Trace{RequestID: "ldap-login"}, LoginInput{Provider: "ldap", Username: "alice", Password: "directory-password"})
	must(t, err)
	if result.User.Username != "directory-alice" || result.User.Role != "Operator" {
		t.Fatalf("LDAP subject mapped incorrectly: %#v", result.User)
	}
	actor, err := f.s.AuthenticateSession(testCtx, raw)
	must(t, err)
	if actor.ID != result.User.ID {
		t.Fatal("LDAP session not bound to mapped application user")
	}
	_, _, err = f.s.Login(testCtx, Trace{}, LoginInput{Provider: "ldap", Username: "alice", Password: testPassword})
	status(t, err, 401) // valid local password never falls back
	_, _, err = f.s.Login(testCtx, Trace{}, LoginInput{Provider: "ldap", Username: "bob", Password: "directory-password"})
	status(t, err, 401) // successfully bound, but no explicit app mapping
	_, _, err = f.s.Login(testCtx, Trace{}, LoginInput{Provider: "local", Username: "directory-alice", Password: "directory-password"})
	status(t, err, 401)
}

const coreLDAPBindRequest = ldap.ApplicationBindRequest
const coreLDAPSearchRequest = ldap.ApplicationSearchRequest
const coreLDAPBindResponse = ldap.ApplicationBindResponse
const coreLDAPSearchEntry = ldap.ApplicationSearchResultEntry
const coreLDAPSearchDone = ldap.ApplicationSearchResultDone
const coreLDAPExtendedRequest = ldap.ApplicationExtendedRequest
const coreLDAPExtendedResponse = ldap.ApplicationExtendedResponse

type coreLDAPFixture struct {
	listener  net.Listener
	tlsConfig *tls.Config
	caFile    string
	secure    bool
}

func newCoreLDAPFixture(t *testing.T, ldaps bool) *coreLDAPFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(11), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &coreLDAPFixture{listener: listener, tlsConfig: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, secure: ldaps}
	f.caFile = t.TempDir() + "/ldap-ca.pem"
	if err = os.WriteFile(f.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	go f.serve()
	return f
}
func (f *coreLDAPFixture) url(scheme string) string {
	return scheme + "://" + f.listener.Addr().String()
}
func (f *coreLDAPFixture) close() { _ = f.listener.Close() }
func (f *coreLDAPFixture) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			secure := false
			if f.secure {
				tc := tls.Server(c, f.tlsConfig)
				if tc.Handshake() != nil {
					_ = tc.Close()
					return
				}
				c = tc
				secure = true
			}
			f.handle(c, secure)
		}(conn)
	}
}
func (f *coreLDAPFixture) handle(conn net.Conn, tlsOn bool) {
	defer conn.Close()
	bound := ""
	for {
		pkt, err := ber.ReadPacket(conn)
		if err != nil {
			return
		}
		if len(pkt.Children) < 2 {
			return
		}
		id, ok := pkt.Children[0].Value.(int64)
		if !ok {
			return
		}
		op := pkt.Children[1]
		if op.ClassType == ber.ClassApplication && int(op.Tag) == coreLDAPExtendedRequest {
			if len(op.Children) < 1 || op.Children[0].Data.String() != "1.3.6.1.4.1.1466.20037" {
				coreLDAPResult(conn, id, coreLDAPExtendedResponse, 2)
				return
			}
			coreLDAPResult(conn, id, coreLDAPExtendedResponse, 0)
			tc := tls.Server(conn, f.tlsConfig)
			if tc.Handshake() != nil {
				return
			}
			conn = tc
			tlsOn = true
			continue
		}
		if op.ClassType != ber.ClassApplication {
			return
		}
		switch int(op.Tag) {
		case coreLDAPBindRequest:
			if len(op.Children) < 3 {
				return
			}
			dn := op.Children[1].Data.String()
			password := string(op.Children[2].Data.Bytes())
			parts := strings.SplitN(strings.TrimPrefix(dn, "uid="), ",", 2)
			if len(parts) == 2 {
				bound = parts[0]
			}
			if len(parts) != 2 || password != "directory-password" {
				coreLDAPResult(conn, id, coreLDAPBindResponse, 49)
			} else {
				coreLDAPResult(conn, id, coreLDAPBindResponse, 0)
			}
		case coreLDAPSearchRequest:
			if !tlsOn || bound == "" {
				return
			}
			entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, coreLDAPSearchEntry, nil, "Search Result Entry")
			entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "uid="+bound+",ou=people,dc=example,dc=com", "Object Name"))
			attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attributes")
			for name, value := range map[string]string{"uid": bound, "entryUUID": "stable-directory-" + bound, "cn": strings.ToUpper(bound)} {
				a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attribute")
				a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "Attribute Name"))
				vals := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "Attribute Values")
				vals.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, value, "Attribute Value"))
				a.AppendChild(vals)
				attrs.AppendChild(a)
			}
			entry.AppendChild(attrs)
			coreLDAPOp(conn, id, entry)
			coreLDAPResult(conn, id, coreLDAPSearchDone, 0)
		default:
			return
		}
	}
}
func coreLDAPResult(c net.Conn, id int64, tag, code int) {
	op := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(tag), nil, "LDAP Result")
	op.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, int64(code), "Result Code"))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "Matched DN"))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "Diagnostic Message"))
	coreLDAPOp(c, id, op)
}
func coreLDAPOp(c net.Conn, id int64, op *ber.Packet) {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP Message")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "Message ID"))
	msg.AppendChild(op)
	_, _ = c.Write(msg.Bytes())
}

type coreOIDCFixture struct {
	server    *httptest.Server
	key       *rsa.PrivateKey
	badKey    *rsa.PrivateKey
	caFile    string
	challenge string
	nonce     string
}

func newCoreOIDCFixture(t *testing.T) *coreOIDCFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &coreOIDCFixture{key: key, badKey: bad}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q,"response_types_supported":["code"],"subject_types_supported":["public"],"id_token_signing_alg_values_supported":["RS256"]}`, server.URL, server.URL+"/authorize", server.URL+"/token", server.URL+"/keys")
		case "/keys":
			pub := f.key.PublicKey
			_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"RSA","use":"sig","kid":"core-key","alg":"RS256","n":%q,"e":%q}]}`, base64.RawURLEncoding.EncodeToString(pub.N.Bytes()), base64.RawURLEncoding.EncodeToString(coreBigEndianInt(pub.E)))
		case "/token":
			_ = r.ParseForm()
			id, secret, ok := r.BasicAuth()
			if !ok || id != "flyway-test" || secret != "oidc-client-secret" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != f.challenge {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			code := r.Form.Get("code")
			if code == "provider_error" {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			claims := map[string]any{"iss": server.URL, "sub": "oidc-subject-alice", "aud": "flyway-test", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": f.nonce, "preferred_username": "alice", "name": "Alice"}
			switch code {
			case "wrong_nonce":
				claims["nonce"] = "bad-nonce"
			case "missing_subject":
				delete(claims, "sub")
			case "unregistered":
				claims["sub"] = "oidc-subject-unregistered"
			}
			priv := f.key
			if code == "bad_signature" {
				priv = f.badKey
			}
			signed, e := coreSignIDToken(priv, claims)
			if e != nil {
				http.Error(w, "token error", 500)
				return
			}
			_, _ = fmt.Fprintf(w, `{"access_token":"opaque-token","token_type":"Bearer","expires_in":300,"id_token":%q}`, signed)
		default:
			http.NotFound(w, r)
		}
	}))
	f.server = server
	f.caFile = t.TempDir() + "/oidc-ca.pem"
	if err = os.WriteFile(f.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}
func coreSignIDToken(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "core-key"})
	payload, e := json.Marshal(claims)
	if e != nil {
		return "", e
	}
	message := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(message))
	sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if e != nil {
		return "", e
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
func coreBigEndianInt(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte(n)}, out...)
		n >>= 8
	}
	return out
}
