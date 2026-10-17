// Package postgresql models the bounded PostgreSQL connection and discovery probe.
package postgresql

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"flyway-manager/internal/oracle"
)

const (
	defaultOverallTimeout = 20 * time.Second
	maxOverallTimeout     = 60 * time.Second
)

var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ValidIdentifier accepts the initial profile's lowercase, unquoted PostgreSQL
// identifier subset for databases, schemas, history tables, and login roles.
func ValidIdentifier(name string) bool { return identifier.MatchString(name) }

// ValidateCA accepts one or more PEM-encoded CA certificates and rejects any
// other PEM blocks or trailing non-whitespace data.
func ValidateCA(ca string) error {
	if len(ca) == 0 || len(ca) > 65536 {
		return errors.New("a PostgreSQL CA bundle of at most 64 KiB is required")
	}
	remaining := []byte(ca)
	count := 0
	for len(strings.TrimSpace(string(remaining))) > 0 {
		if !strings.HasPrefix(strings.TrimSpace(string(remaining)), "-----BEGIN CERTIFICATE-----") {
			return errors.New("PostgreSQL trust input must contain only PEM CA certificates")
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return errors.New("PostgreSQL trust input must contain only PEM CA certificates")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid {
			return errors.New("PostgreSQL trust input must contain valid CA certificates")
		}
		count++
		remaining = rest
	}
	if count == 0 {
		return errors.New("a PostgreSQL CA certificate is required")
	}
	return nil
}

type Config struct {
	Host           string
	Port           int
	DatabaseName   string
	Username       string
	Password       string
	CAPEM          string
	OverallTimeout time.Duration
}

type Result struct {
	oracle.Result
	DatabaseName  string `json:"database_name"`
	ServerVersion string `json:"server_version"`
	SessionUser   string `json:"session_user"`
	TLSMode       string `json:"tls_mode"`
	JDBCDriver    string `json:"jdbc_driver"`
}

type SchemaMetadata struct {
	Name   string
	Owner  string
	Create bool
}

type Metadata struct {
	DatabaseName  string
	SessionUser   string
	CurrentUser   string
	ServerVersion string
	Schemas       []SchemaMetadata
}

func BoundedTimeout(c Config) time.Duration {
	t := c.OverallTimeout
	if t <= 0 {
		return defaultOverallTimeout
	}
	if t > maxOverallTimeout {
		return maxOverallTimeout
	}
	return t
}

func Validate(c Config) error {
	if strings.TrimSpace(c.Host) == "" || len(c.Host) > 253 || c.Port < 1 || c.Port > 65535 || !ValidIdentifier(c.DatabaseName) || !ValidIdentifier(c.Username) || c.Password == "" || len([]byte(c.Password)) > 4096 || !utf8.ValidString(c.Password) || strings.ContainsRune(c.Password, '\x00') || strings.ContainsAny(c.Password, "\r\n\u0085\u2028\u2029") {
		return errors.New("enter valid PostgreSQL connection details and a password of at most 4096 bytes")
	}
	if ip := net.ParseIP(c.Host); ip == nil {
		if strings.ContainsAny(c.Host, "()/:\\ \t\r\n") {
			return errors.New("enter a valid PostgreSQL host name or IP address")
		}
		for _, label := range strings.Split(strings.TrimSuffix(c.Host, "."), ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return errors.New("enter a valid PostgreSQL host name or IP address")
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
					return errors.New("enter a valid PostgreSQL host name or IP address")
				}
			}
		}
	}
	return ValidateCA(c.CAPEM)
}

func UnavailableResult() Result {
	return Result{Result: oracle.Result{NetworkState: "unknown", AuthenticationState: "unknown", ErrorCode: "POSTGRESQL_PROBE_UNAVAILABLE", ErrorMessage: "A verified PostgreSQL JDBC runtime is required for discovery.", Schemas: []oracle.SchemaCandidate{}, SystemPrivileges: []string{}, EnabledRoles: []string{}, Capabilities: []oracle.Capability{}, Warnings: []string{}}, TLSMode: "verify-full", JDBCDriver: "PostgreSQL JDBC 42.7.14"}
}

// Probe is deliberately unavailable outside the verified Flyway runtime.
func Probe(ctx context.Context, c Config) (Result, error) {
	_ = ctx
	if err := Validate(c); err != nil {
		return Result{}, err
	}
	return UnavailableResult(), nil
}

func ResultFromMetadata(expected Config, m Metadata) (Result, error) {
	result := UnavailableResult()
	result.NetworkState = "verified"
	result.ErrorCode = ""
	result.ErrorMessage = ""
	result.DatabaseName = m.DatabaseName
	result.SessionUser = m.SessionUser
	result.ServerVersion = m.ServerVersion
	result.ActualUser = m.SessionUser
	result.Schemas = make([]oracle.SchemaCandidate, 0, len(m.Schemas))
	if m.DatabaseName != expected.DatabaseName || m.SessionUser != expected.Username || m.CurrentUser != m.SessionUser {
		result.AuthenticationState = "rejected"
		result.ErrorCode = "POSTGRESQL_IDENTITY_MISMATCH"
		result.ErrorMessage = "The authenticated PostgreSQL database or session role does not match the requested target."
		return result, nil
	}
	result.AuthenticationState = "verified"
	version, versionErr := strconv.Atoi(m.ServerVersion)
	if versionErr != nil || version < 170000 || version >= 180000 {
		result.Schemas = []oracle.SchemaCandidate{}
		result.SchemaTotal = 0
		result.ErrorCode = "POSTGRESQL_VERSION_UNSUPPORTED"
		result.ErrorMessage = "The connected server is not PostgreSQL 17."
		return result, nil
	}
	for _, schema := range m.Schemas {
		if !identifier.MatchString(schema.Name) || schema.Name == "information_schema" || strings.HasPrefix(schema.Name, "pg_") {
			continue
		}
		privileges := []string{"USAGE"}
		capability, reason := "unknown", "CREATE privilege was not established by the read-only probe."
		if schema.Create {
			privileges = append(privileges, "CREATE")
			capability, reason = "potential", "The session role has CREATE privilege; migration behavior has not been acceptance tested."
		}
		result.Schemas = append(result.Schemas, oracle.SchemaCandidate{Name: schema.Name, Own: schema.Owner == m.CurrentUser, KnownPrivileges: privileges, MigrationCapability: capability, CapabilityReason: reason})
	}
	result.SchemaTotal = len(result.Schemas)
	result.Capabilities = []oracle.Capability{{View: "PG_IDENTITY", Status: "complete", Detail: "Database, login role, current role and server version verified."}, {View: "PG_SCHEMAS", Status: "complete", Detail: "Only existing non-system schemas with USAGE are listed."}}
	return result, nil
}
