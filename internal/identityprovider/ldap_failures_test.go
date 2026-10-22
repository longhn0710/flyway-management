package identityprovider

import (
	"context"
	"testing"
	"time"
)

func TestLDAPRejectsExpiredTrustedCertificate(t *testing.T) {
	for _, transport := range []struct {
		name  string
		ldaps bool
		start bool
	}{
		{name: "ldaps", ldaps: true},
		{name: "ldap", start: true},
	} {
		t.Run(transport.name, func(t *testing.T) {
			now := time.Now()
			fixture := newLDAPFixtureWithValidity(t, transport.ldaps, now.Add(-2*time.Hour), now.Add(-time.Hour))
			defer fixture.close()
			cfg := fixture.config(transport.name, transport.start, fixture.caFile)
			identity, err := AuthenticateLDAP(context.Background(), cfg, "alice", "correct horse")
			if err == nil || err.Error() != "LDAP authentication failed" || identity != (Identity{}) {
				t.Fatal("expired trusted certificate did not fail closed with a sanitized error and empty identity")
			}
		})
	}
}

func TestLDAPDirectoryOutageDoesNotReuseAuthenticatedIdentity(t *testing.T) {
	fixture := newLDAPFixture(t, true)
	defer fixture.close()
	cfg := fixture.config("ldaps", false, fixture.caFile)
	identity, err := AuthenticateLDAP(context.Background(), cfg, "alice", "correct horse")
	if err != nil || identity.Subject != "immutable-9f" {
		t.Fatal("directory outage precondition did not authenticate")
	}
	fixture.close()
	identity, err = AuthenticateLDAP(context.Background(), cfg, "alice", "correct horse")
	if err == nil || err.Error() != "LDAP authentication failed" || identity != (Identity{}) {
		t.Fatal("offline directory reused an identity or failed to return a sanitized error")
	}
}
