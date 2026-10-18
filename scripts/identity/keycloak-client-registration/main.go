// Operator-only, single-create registration for the explicitly approved disabled client.
// Credentials enter only through stdin/runtime env; output contains fixed metadata only.
package main

import (
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"time"
)

const origin = "https://auth.apps.drgdevlab.com"
const base = "/admin/realms/kubernetes"
const proposal = `{"clientId":"flyway-manager","enabled":false,"protocol":"openid-connect","clientAuthenticatorType":"client-secret","publicClient":false,"standardFlowEnabled":true,"implicitFlowEnabled":false,"directAccessGrantsEnabled":false,"serviceAccountsEnabled":false,"redirectUris":["https://flyway-manager.apps.drgdevlab.com/api/v1/auth/oidc/callback"],"webOrigins":["https://flyway-manager.apps.drgdevlab.com"],"attributes":{"pkce.code.challenge.method":"S256"}}`

func validateInput(data []byte) (map[string]any, string, bool) {
	var value, expected map[string]any
	if json.Unmarshal(data, &value) != nil || json.Unmarshal([]byte(proposal), &expected) != nil {
		return nil, "", false
	}
	secret, ok := value["secret"].(string)
	delete(value, "secret")
	if !ok || len(secret) != 64 || !reflect.DeepEqual(value, expected) {
		return nil, "", false
	}
	for _, c := range secret {
		if !((c >= 'a' && c <= 'f') || (c >= '0' && c <= '9')) {
			return nil, "", false
		}
	}
	value["secret"] = secret
	return value, secret, true
}

func approvedAttributes(actual any) bool {
	attrs, ok := actual.(map[string]any)
	if !ok || attrs["pkce.code.challenge.method"] != "S256" {
		return false
	}
	defaults := map[string]string{"backchannel.logout.revoke.offline.tokens": "false", "backchannel.logout.session.required": "true", "realm_client": "false"}
	for k, v := range attrs {
		if k == "pkce.code.challenge.method" {
			continue
		}
		want, known := defaults[k]
		if !known || v != want {
			return false
		}
	}
	return true
}

type api struct {
	client *http.Client
	token  string
}

func (a *api) call(method, path, contentType string, body []byte) (int, []byte, error) {
	r, err := http.NewRequest(method, origin+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if a.token != "" {
		r.Header.Set("Authorization", "Bearer "+a.token)
	}
	response, err := a.client.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if len(data) > 4*1024*1024 {
		return response.StatusCode, nil, io.ErrUnexpectedEOF
	}
	return response.StatusCode, data, err
}

func (a *api) get(path string, out any) error {
	status, data, err := a.call("GET", path, "", nil)
	if err != nil || status != 200 {
		return fmt.Errorf("bounded_read_failed_http_%d", status)
	}
	if json.Unmarshal(data, out) != nil {
		return fmt.Errorf("invalid_response")
	}
	return nil
}

// Compare existing definitions in memory; never serialize them into evidence.
func (a *api) snapshot() (map[string]any, error) {
	result := map[string]any{}
	for _, suffix := range []string{"", "/clients", "/users?first=0&max=1000", "/groups?first=0&max=1000", "/roles"} {
		var value any
		if err := a.get(base+suffix, &value); err != nil {
			return nil, fmt.Errorf("%s: %w", suffix, err)
		}
		if suffix == "/clients" {
			items, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("invalid_clients")
			}
			filtered := map[string]any{}
			for _, item := range items {
				c, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid_client")
				}
				if c["clientId"] != "flyway-manager" {
					id, ok := c["id"].(string)
					if !ok {
						return nil, fmt.Errorf("invalid_id")
					}
					filtered[id] = c
				}
			}
			value = filtered
		}
		result[suffix] = value
	}
	return result, nil
}

func main() {
	report := map[string]any{"status": "NOT_RUN", "realm": "kubernetes", "client_id": "flyway-manager", "client_create_attempts": 0, "kubernetes_secret_values_read_or_decoded": false}
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(report) }()
	if len(os.Args) != 2 || (os.Args[1] != "--create-approved-disabled-client" && os.Args[1] != "--verify-approved-disabled-client") {
		report["stage"] = "explicit_operation_required"
		return
	}
	create := os.Args[1] == "--create-approved-disabled-client"
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 8193))
	value, secret, valid := validateInput(data)
	if err != nil || len(data) > 8192 || !valid {
		report["stage"] = "proposal_validation"
		return
	}
	username, password := os.Getenv("KC_BOOTSTRAP_ADMIN_USERNAME"), os.Getenv("KC_BOOTSTRAP_ADMIN_PASSWORD")
	if username == "" || password == "" {
		report["stage"] = "runtime_credential_absent"
		return
	}
	a := &api{client: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 4 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	defer a.client.CloseIdleConnections()
	fail := func(stage string) { report["status"] = "NOT_VERIFIED"; report["stage"] = stage }
	form := url.Values{"client_id": {"admin-cli"}, "grant_type": {"password"}, "username": {username}, "password": {password}}
	status, body, err := a.call("POST", "/realms/master/protocol/openid-connect/token", "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil || status != 200 {
		fail("admin_authentication")
		return
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if json.Unmarshal(body, &token) != nil || token.Access == "" || token.Refresh == "" {
		fail("admin_token_response")
		return
	}
	a.token = token.Access
	defer func() {
		a.token = ""
		s, _, e := a.call("POST", "/realms/master/protocol/openid-connect/logout", "application/x-www-form-urlencoded", []byte(url.Values{"client_id": {"admin-cli"}, "refresh_token": {token.Refresh}}.Encode()))
		report["own_admin_session_logged_out"] = e == nil && s == 204
	}()
	var matches []map[string]any
	if a.get(base+"/clients?clientId=flyway-manager", &matches) != nil {
		fail("absence_query")
		return
	}
	if create && len(matches) != 0 {
		report["stage"] = "existing_client_no_overwrite"
		return
	}
	var before map[string]any
	if create {
		before, err = a.snapshot()
		if err != nil {
			report["preservation_read_failure"] = err.Error()
			fail("preservation_preflight")
			return
		}
		payload, _ := json.Marshal(value)
		report["client_create_attempts"] = 1
		status, _, err = a.call("POST", base+"/clients", "application/json", payload)
		report["create_http_status"] = status
		report["create_transport_completed"] = err == nil
	}
	// No mutation retry, including after ambiguous transport failure. Reconcile by reads.
	if a.get(base+"/clients?clientId=flyway-manager", &matches) != nil || len(matches) != 1 {
		fail("post_create_query")
		return
	}
	c := matches[0]
	id, ok := c["id"].(string)
	if !ok || id == "" {
		fail("created_id")
		return
	}
	report["client_uuid"] = id
	delete(value, "secret")
	mismatches := []string{}
	for k, want := range value {
		if k == "attributes" && approvedAttributes(c[k]) {
			continue
		}
		if !reflect.DeepEqual(c[k], want) {
			mismatches = append(mismatches, k)
		}
	}
	if len(mismatches) > 0 {
		report["mismatched_fields"] = mismatches
		if attrs, ok := c["attributes"].(map[string]any); ok {
			keys := []string{}
			for k := range attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			report["returned_attribute_keys"] = keys
			for _, k := range []string{"pkce.code.challenge.method", "backchannel.logout.revoke.offline.tokens", "backchannel.logout.session.required", "realm_client"} {
				if v, ok := attrs[k].(string); ok {
					report["attribute_"+k] = v
				}
			}
		}
		fail("created_configuration_mismatch")
		return
	}
	var credential struct {
		Value string `json:"value"`
	}
	if a.get(base+"/clients/"+url.PathEscape(id)+"/client-secret", &credential) != nil || subtle.ConstantTimeCompare([]byte(secret), []byte(credential.Value)) != 1 {
		fail("generated_secret_match")
		return
	}
	if create {
		after, err := a.snapshot()
		if err != nil {
			fail("preservation_postcheck")
			return
		}
		report["existing_definitions_equal"] = reflect.DeepEqual(before, after)
		if !reflect.DeepEqual(before, after) {
			fail("existing_definitions_changed")
			return
		}
	}
	report["status"] = "PASS"
	report["stage"] = "disabled_client_registered"
	report["disabled"] = true
	report["exact_approved_configuration"] = true
	report["only_observed_safe_server_attribute_defaults"] = true
	report["generated_secret_matches"] = true
}
