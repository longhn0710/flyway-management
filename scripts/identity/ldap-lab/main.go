// Operator-only acceptance against the approved isolated LDAP directory.
// No Kubernetes Secret reads; no credential values in arguments or output.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"flyway-manager/internal/identityprovider"
	"github.com/go-ldap/ldap/v3"
)

const suffix = "dc=flyway,dc=lab"
const people = "ou=people," + suffix
const viewerGroup = "cn=flyway-viewer,ou=groups," + suffix

func output(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
func fail(stage string, err error) {
	code := uint16(0)
	if e, ok := err.(*ldap.Error); ok {
		code = e.ResultCode
	}
	output(map[string]any{"status": "FAIL", "stage": stage, "ldap_code": code})
	os.Exit(1)
}
func private(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		fail("protected_input", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fail("protected_input", err)
	}
	return strings.TrimRight(string(b), "\r\n")
}
func main() {
	dir := flag.String("operator-directory", "", "protected original operator material directory")
	endpoint := flag.String("endpoint", "ldaps://127.0.0.1:1636", "approved LDAPS loopback forward")
	op := flag.String("operation", "inspect", "inspect/native/create/remove-group/delete/add-alias/remove-alias/readonly-denial")
	uid := flag.String("uid", "", "disposable fixture UID; native also accepts original fixture users")
	pwd := flag.String("password-file", "", "protected original or disposable user password file")
	wrong := flag.Bool("wrong-password", false, "negative fixture only")
	system := flag.Bool("system-trust-only", false, "negative fixture only")
	flag.Parse()
	u, err := url.Parse(*endpoint)
	if err != nil || u.Scheme != "ldaps" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "openldap.flyway-manager-identity-lab.svc.cluster.local") {
		fail("approved_ldaps_endpoint", nil)
	}
	caPath := filepath.Join(*dir, "ca.crt")
	if *op == "native" {
		password := private(*pwd)
		if *wrong {
			password = "synthetic-wrong-password"
		}
		cfg := identityprovider.LDAPConfig{URL: *endpoint, BaseDN: people, BindDNTemplate: "uid={username}," + people, UsernameAttribute: "uid", SubjectAttribute: "entryUUID", DisplayNameAttr: "cn", CAFile: caPath}
		if *system {
			cfg.CAFile = ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		identity, e := identityprovider.AuthenticateLDAP(ctx, cfg, *uid, password)
		reason := ""
		if e != nil {
			switch e.Error() {
			case "invalid credentials":
				reason = "bind_denied"
			case "LDAP identity lookup failed":
				reason = "identity_lookup_denied"
			case "LDAP authentication failed":
				reason = "transport_or_cancellation"
			default:
				reason = "provider_configuration_rejected"
			}
		}
		output(map[string]any{"status": "OBSERVED", "authenticated": e == nil, "reason": reason, "subject": identity.Subject, "username": identity.Username})
		return
	}
	if *op != "inspect" && *op != "configure-identity-acl" && !regexp.MustCompile(`^phase9-retire-[a-f0-9]{12}$`).MatchString(*uid) {
		fail("disposable_uid_required", nil)
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		fail("ca_file", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		fail("ca_parse", nil)
	}
	ldap.DefaultTimeout = 5 * time.Second
	conn, err := ldap.DialURL(*endpoint, ldap.DialWithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "openldap.flyway-manager-identity-lab.svc.cluster.local"}))
	if err != nil {
		fail("verified_ldaps", err)
	}
	defer conn.Close()
	conn.SetTimeout(5 * time.Second)
	readonly := *op == "inspect" || *op == "readonly-denial"
	bindDN := "cn=admin," + suffix
	bindFile := "data-root-password.trimmed"
	if *op == "configure-identity-acl" {
		bindDN = "cn=admin,cn=config"
		bindFile = "config-root-password.trimmed"
	}
	if readonly {
		bindDN = "cn=readonly," + suffix
		bindFile = "readonly-bind-password.trimmed"
	}
	if err = conn.Bind(bindDN, private(filepath.Join(*dir, bindFile))); err != nil {
		fail("bind", err)
	}
	dn := "uid=" + *uid + "," + people
	switch *op {
	case "configure-identity-acl":
		result, e := conn.Search(ldap.NewSearchRequest("cn=config", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 4, false, "(olcSuffix="+suffix+")", []string{"olcAccess"}, nil))
		if e != nil || len(result.Entries) != 1 {
			fail("isolated_database_acl", e)
		}
		entry := result.Entries[0]
		old := entry.GetAttributeValues("olcAccess")
		updated := []string{}
		foundPassword := false
		foundUUID := false
		strip := regexp.MustCompile(`^\{[0-9]+\}`)
		for _, value := range old {
			rule := strip.ReplaceAllString(value, "")
			if strings.HasPrefix(rule, "to attrs=entryUUID ") {
				foundUUID = true
				continue
			}
			if strings.HasPrefix(rule, "to attrs=userPassword,shadowLastChange ") {
				foundPassword = true
				if strings.HasSuffix(rule, "by * break") {
					rule = strings.TrimSuffix(rule, "by * break") + "by * none"
				} else if !strings.HasSuffix(rule, "by * none") {
					fail("unexpected_password_acl", nil)
				}
				updated = append(updated, rule, "to attrs=entryUUID by self read by * break")
				continue
			}
			updated = append(updated, rule)
		}
		if !foundPassword {
			fail("expected_password_acl_missing", nil)
		}
		for i := range updated {
			updated[i] = fmt.Sprintf("{%d}%s", i, updated[i])
		}
		mod := ldap.NewModifyRequest(entry.DN, nil)
		mod.Replace("olcAccess", updated)
		if e = conn.Modify(mod); e != nil {
			fail("identity_acl_update", e)
		}
		output(map[string]any{"status": "PASS", "self_uuid_read": true, "other_password_hash_read_denied": true, "previous_uuid_rule_present": foundUUID})
		return
	case "inspect":
		result, e := conn.Search(ldap.NewSearchRequest(suffix, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 20, 4, false, "(|(objectClass=inetOrgPerson)(objectClass=groupOfNames)(objectClass=organizationalUnit))", []string{"uid", "cn", "entryUUID", "member"}, nil))
		if e != nil {
			fail("bounded_readonly_search", e)
		}
		state, _ := conn.TLSConnectionState()
		entries := []map[string]any{}
		for _, entry := range result.Entries {
			entries = append(entries, map[string]any{"dn": entry.DN, "uid": entry.GetAttributeValue("uid"), "uuid": entry.GetAttributeValue("entryUUID"), "members": entry.GetAttributeValues("member")})
		}
		output(map[string]any{"status": "PASS", "tls13": state.Version == tls.VersionTLS13, "verified_service_hostname": true, "readonly_bind": true, "entries": entries})
		return
	case "create":
		add := ldap.NewAddRequest(dn, nil)
		add.Attribute("objectClass", []string{"top", "person", "organizationalPerson", "inetOrgPerson"})
		add.Attribute("uid", []string{*uid})
		add.Attribute("cn", []string{"Phase9 Disposable Viewer"})
		add.Attribute("sn", []string{"Acceptance"})
		if err = conn.Add(add); err != nil {
			fail("create_disposable_user", err)
		}
		if _, err = conn.PasswordModify(ldap.NewPasswordModifyRequest(dn, "", private(*pwd))); err != nil {
			fail("disposable_password_hashing_user_retained", err)
		}
		mod := ldap.NewModifyRequest(viewerGroup, nil)
		mod.Add("member", []string{dn})
		if err = conn.Modify(mod); err != nil {
			fail("add_disposable_membership_user_retained", err)
		}
	case "remove-group":
		mod := ldap.NewModifyRequest(viewerGroup, nil)
		mod.Delete("member", []string{dn})
		if err = conn.Modify(mod); err != nil {
			fail("remove_disposable_membership", err)
		}
	case "delete":
		found, e := conn.Search(ldap.NewSearchRequest(viewerGroup, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 4, false, "(objectClass=*)", []string{"member"}, nil))
		if e != nil {
			fail("membership_precondition", e)
		}
		for _, entry := range found.Entries {
			for _, member := range entry.GetAttributeValues("member") {
				if strings.EqualFold(member, dn) {
					fail("remove_membership_before_delete", nil)
				}
			}
		}
		if err = conn.Del(ldap.NewDelRequest(dn, nil)); err != nil {
			fail("delete_disposable_user", err)
		}
	case "add-alias", "remove-alias":
		mod := ldap.NewModifyRequest(dn, nil)
		if *op == "add-alias" {
			mod.Add("uid", []string{"phase9-viewer"})
		} else {
			mod.Delete("uid", []string{"phase9-viewer"})
		}
		if err = conn.Modify(mod); err != nil {
			fail("disposable_alias", err)
		}
	case "readonly-denial":
		mod := ldap.NewModifyRequest(dn, nil)
		mod.Replace("description", []string{"unexpected-readonly-write"})
		err = conn.Modify(mod)
		if !ldap.IsErrorWithCode(err, ldap.LDAPResultInsufficientAccessRights) {
			fail("readonly_write_not_denied", err)
		}
		result, e := conn.Search(ldap.NewSearchRequest(people, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 4, false, "(uid=phase9-viewer)", []string{"userPassword"}, nil))
		if e != nil || len(result.Entries) != 1 || len(result.Entries[0].GetAttributeValues("userPassword")) != 0 {
			fail("readonly_password_attribute_not_denied", e)
		}
	default:
		fail("unknown_operation", nil)
	}
	output(map[string]any{"status": "PASS", "operation": *op, "uid": *uid})
}
