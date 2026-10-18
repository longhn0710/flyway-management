package core

import (
	"net/url"
	"testing"
)

func TestOIDCPendingFlowRejectedAfterProviderDisableOrReplacement(t *testing.T) {
	for _, mode := range []string{"disable", "replace"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			before := newCoreOIDCFixture(t)
			defer before.server.Close()
			after := newCoreOIDCFixture(t)
			defer after.server.Close()
			settings := AuthSettings{LocalEnabled: true, OIDC: OIDCSettings{Enabled: true, Issuer: before.server.URL, ClientID: "flyway-test", ClientSecret: "oidc-client-secret", RedirectURL: "https://manager.example/api/v1/auth/oidc/callback", CAFile: before.caFile}}
			_, err := f.s.UpdateSettings(testCtx, f.trace(), settings)
			must(t, err)
			oldUser, err := f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "oidc-before", Provider: "oidc", Issuer: before.server.URL, Subject: "oidc-subject-alice", Role: "Viewer"})
			must(t, err)
			newUser, err := f.s.CreateUser(testCtx, f.trace(), UserInput{Username: "oidc-after", Provider: "oidc", Issuer: after.server.URL, Subject: "oidc-subject-alice", Role: "Operator"})
			must(t, err)
			start := func(idp *coreOIDCFixture) (string, string) {
				location, cookie, e := f.s.OIDCStart(testCtx)
				must(t, e)
				u, e := url.Parse(location)
				must(t, e)
				idp.challenge, idp.nonce = u.Query().Get("code_challenge"), u.Query().Get("nonce")
				return u.Query().Get("state"), cookie
			}
			state, cookie := start(before)
			// Both authorities can issue a valid token for the same browser challenge;
			// a callback must still belong to the configuration that began its flow.
			after.challenge, after.nonce = before.challenge, before.nonce
			changed := settings
			if mode == "disable" {
				changed.OIDC.Enabled = false
			} else {
				changed.OIDC.Issuer, changed.OIDC.CAFile = after.server.URL, after.caFile
			}
			_, err = f.s.UpdateSettings(testCtx, f.trace(), changed)
			must(t, err)
			result, raw, err := f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "good")
			status(t, err, 401)
			if raw != "" || result.User.ID != "" {
				t.Fatal("changed provider configuration issued a session from a stale flow")
			}
			if mode == "disable" {
				_, _, err = f.s.OIDCStart(testCtx)
				status(t, err, 503)
			}
			_, err = f.s.UpdateSettings(testCtx, f.trace(), settings)
			must(t, err)
			_, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "good")
			status(t, err, 401) // Restoring settings cannot resurrect a consumed flow.
			var sessions int
			must(t, f.db.SQL.QueryRow("SELECT count(*) FROM sessions WHERE user_id IN (?,?)", oldUser.ID, newUser.ID).Scan(&sessions))
			if sessions != 0 {
				t.Fatal("rejected or replayed callback retained an external session")
			}
			expected, idp := oldUser, before
			if mode == "replace" {
				_, err = f.s.UpdateSettings(testCtx, f.trace(), changed)
				must(t, err)
				expected, idp = newUser, after
			}
			state, cookie = start(idp)
			result, _, err = f.s.OIDCCallback(testCtx, Trace{}, state, cookie, "good")
			must(t, err)
			if result.User.ID != expected.ID || result.User.Role != expected.Role {
				t.Fatal("a fresh flow did not map to the current authority's registered identity")
			}
			admin, err := f.s.AuthenticateSession(testCtx, f.session)
			must(t, err)
			if !admin.Admin() {
				t.Fatal("OIDC settings changes invalidated local Admin access")
			}
		})
	}
}
