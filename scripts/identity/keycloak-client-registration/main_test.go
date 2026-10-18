package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestServerDefaultsCannotRelaxPKCEOrAddAuthority(t *testing.T) {
	if !approvedAttributes(map[string]any{"pkce.code.challenge.method": "S256", "realm_client": "false", "backchannel.logout.session.required": "true", "backchannel.logout.revoke.offline.tokens": "false"}) {
		t.Fatal("observed inert defaults rejected")
	}
	for _, attrs := range []map[string]any{
		{"pkce.code.challenge.method": "plain"},
		{"pkce.code.challenge.method": "S256", "realm_client": "true"},
		{"pkce.code.challenge.method": "S256", "oauth2.device.authorization.grant.enabled": "true"},
		{"pkce.code.challenge.method": "S256", "backchannel.logout.session.required": "false"},
	} {
		if approvedAttributes(attrs) {
			t.Fatal("unexpected authority/default accepted")
		}
	}
}

func TestRegistrationRejectsBroaderOrIncompleteAuthority(t *testing.T) {
	data, err := os.ReadFile("../../../deploy/identity/keycloak-client-proposal.json")
	if err != nil {
		t.Fatal(err)
	}
	var approved, embedded map[string]any
	if json.Unmarshal(data, &approved) != nil || json.Unmarshal([]byte(proposal), &embedded) != nil || !reflect.DeepEqual(approved, embedded) {
		t.Fatal("embedded contract differs from reviewed proposal")
	}
	cases := map[string]func(map[string]any){
		"enabled":           func(m map[string]any) { m["enabled"] = true },
		"wildcard_callback": func(m map[string]any) { m["redirectUris"] = []string{"https://flyway-manager.apps.drgdevlab.com/*"} },
		"password_grant":    func(m map[string]any) { m["directAccessGrantsEnabled"] = true },
		"service_account":   func(m map[string]any) { m["serviceAccountsEnabled"] = true },
		"extra_mapper":      func(m map[string]any) { m["protocolMappers"] = []any{} },
		"absent_pkce":       func(m map[string]any) { delete(m, "attributes") },
		"absent_secret":     func(m map[string]any) { delete(m, "secret") },
		"short_secret":      func(m map[string]any) { m["secret"] = "short" },
		"unsafe_secret":     func(m map[string]any) { m["secret"] = strings.Repeat("\n", 64) },
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			m["secret"] = strings.Repeat("a", 64)
			modify(m)
			encoded, _ := json.Marshal(m)
			if _, _, ok := validateInput(encoded); ok {
				t.Fatal("broader/incomplete input accepted")
			}
		})
	}
	approved["secret"] = strings.Repeat("a", 64)
	encoded, _ := json.Marshal(approved)
	if _, _, ok := validateInput(encoded); !ok {
		t.Fatal("approved disabled contract rejected")
	}
}
