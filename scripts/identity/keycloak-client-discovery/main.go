// Operator-only, read-only client discovery. Run inside the existing Keycloak
// workload so its runtime credential is never returned to the operator or placed in argv.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const origin = "https://auth.apps.drgdevlab.com"
const clientID = "flyway-manager"
const callback = "https://flyway-manager.apps.drgdevlab.com/api/v1/auth/oidc/callback"

func transportFailure(err error) string {
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var network net.Error
	switch {
	case errors.As(err, &unknown):
		return "untrusted_server_certificate"
	case errors.As(err, &hostname):
		return "hostname_validation_failed"
	case errors.As(err, &network) && network.Timeout():
		return "transport_timeout"
	default:
		return "transport_failure"
	}
}

type client struct {
	ID         string            `json:"id"`
	ClientID   string            `json:"clientId"`
	Enabled    bool              `json:"enabled"`
	Public     bool              `json:"publicClient"`
	Standard   bool              `json:"standardFlowEnabled"`
	Implicit   bool              `json:"implicitFlowEnabled"`
	Password   bool              `json:"directAccessGrantsEnabled"`
	Service    bool              `json:"serviceAccountsEnabled"`
	Redirects  []string          `json:"redirectUris"`
	Attributes map[string]string `json:"attributes"`
}

func main() {
	report := map[string]any{"realm": "kubernetes", "client_id": clientID, "realm_or_client_changed": false, "secret_api_values_read_or_decoded": false, "credential_source": "existing workload runtime environment; values never returned"}
	httpClient := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 4 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer httpClient.CloseIdleConnections()
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(report) }()
	call := func(method, path, token string, form url.Values) (int, []byte, error) {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		request, err := http.NewRequest(method, origin+path, body)
		if err != nil {
			return 0, nil, err
		}
		if form != nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := httpClient.Do(request)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		if len(data) > 65536 {
			return response.StatusCode, nil, io.ErrUnexpectedEOF
		}
		return response.StatusCode, data, err
	}
	if len(os.Args) != 2 || (os.Args[1] != "--transport-only" && os.Args[1] != "--read-client") {
		report["status"], report["stage"] = "NOT_RUN", "explicit_operation_required"
		return
	}
	if os.Args[1] == "--transport-only" {
		status, _, err := call("GET", "/realms/kubernetes/.well-known/openid-configuration", "", nil)
		report["stage"], report["http_status"] = "public_discovery_only", status
		report["runtime_credential_consumed"] = false
		if err != nil {
			report["status"], report["failure_class"] = "NOT_VERIFIED", transportFailure(err)
		} else {
			report["status"] = "OBSERVED"
		}
		return
	}
	username, password := os.Getenv("KC_BOOTSTRAP_ADMIN_USERNAME"), os.Getenv("KC_BOOTSTRAP_ADMIN_PASSWORD")
	if username == "" || password == "" {
		report["status"] = "NOT_RUN"
		report["stage"] = "runtime_credential_absent"
		return
	}
	started := time.Now()
	status, body, err := call("POST", "/realms/master/protocol/openid-connect/token", "", url.Values{"client_id": {"admin-cli"}, "grant_type": {"password"}, "username": {username}, "password": {password}})
	report["admin_authentication_duration_ms"] = time.Since(started).Milliseconds()
	if err != nil || status != 200 {
		report["status"] = "NOT_VERIFIED"
		report["stage"] = "admin_authentication"
		report["http_status"] = status
		if err != nil {
			report["failure_class"] = transportFailure(err)
		}
		return
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if json.Unmarshal(body, &token) != nil || token.Access == "" {
		report["status"] = "NOT_VERIFIED"
		report["stage"] = "admin_token_response"
		return
	}
	if token.Refresh != "" {
		defer func() {
			status, _, err := call("POST", "/realms/master/protocol/openid-connect/logout", "", url.Values{"client_id": {"admin-cli"}, "refresh_token": {token.Refresh}})
			report["own_admin_session_logged_out"] = err == nil && status == 204
		}()
	}
	status, body, err = call("GET", "/admin/realms/kubernetes/clients?clientId="+url.QueryEscape(clientID), token.Access, nil)
	if err != nil || status != 200 {
		report["status"] = "NOT_VERIFIED"
		report["stage"] = "exact_client_query"
		report["http_status"] = status
		return
	}
	var clients []client
	if json.Unmarshal(body, &clients) != nil {
		report["status"] = "NOT_VERIFIED"
		report["stage"] = "client_response"
		return
	}
	matches := []map[string]any{}
	for _, c := range clients {
		if c.ClientID == clientID {
			exact, wildcard := false, false
			for _, redirect := range c.Redirects {
				exact = exact || redirect == callback
				wildcard = wildcard || strings.Contains(redirect, "*")
			}
			matches = append(matches, map[string]any{"id": c.ID, "enabled": c.Enabled, "public_client": c.Public, "authorization_code": c.Standard, "implicit": c.Implicit, "password_grant": c.Password, "service_accounts": c.Service, "redirect_count": len(c.Redirects), "canonical_callback_registered": exact, "wildcard_redirect_present": wildcard, "pkce_s256": c.Attributes["pkce.code.challenge.method"] == "S256"})
		}
	}
	report["status"] = "OBSERVED"
	report["matching_clients"] = matches
	report["matching_count"] = len(matches)
}
