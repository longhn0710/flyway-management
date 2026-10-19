// Package oracle provides the read-only metadata model and classifier for the
// Oracle discovery adapter. It deliberately never tests migration DDL.
package oracle

import (
	"context"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultConnectTimeout = 5 * time.Second
	defaultReadTimeout    = 5 * time.Second
	defaultOverallTimeout = 20 * time.Second
	maxConnectTimeout     = 15 * time.Second
	maxReadTimeout        = 10 * time.Second
	maxOverallTimeout     = 60 * time.Second
)

var safeService = regexp.MustCompile(`^[A-Za-z0-9_.$#-]{1,128}$`)
var unquotedSchemaIdentifier = regexp.MustCompile(`^[A-Z][A-Z0-9_$#]{0,127}$`)

// Oracle releases that do not expose ORACLE_MAINTAINED still have a small
// stable set of built-in accounts that must never be presented as targets.
var builtInSchemas = map[string]struct{}{
	"ANONYMOUS": {}, "APEX_PUBLIC_USER": {}, "APPQOSSYS": {}, "AUDSYS": {},
	"CTXSYS": {}, "DBSNMP": {}, "DIP": {}, "DVSYS": {}, "GSMADMIN_INTERNAL": {},
	"LBACSYS": {}, "MDSYS": {}, "OJVMSYS": {}, "OLAPSYS": {}, "ORACLE_OCM": {},
	"ORDDATA": {}, "ORDPLUGINS": {}, "ORDSYS": {}, "OUTLN": {}, "REMOTE_SCHEDULER_AGENT": {},
	"SI_INFORMTN_SCHEMA": {}, "SYS": {}, "SYSBACKUP": {}, "SYSDG": {}, "SYSKM": {},
	"SYSRAC": {}, "SYSTEM": {}, "WMSYS": {}, "XDB": {}, "PUBLIC": {},
}

// Config contains transient connection inputs. The password is used only for
// the probe and must not be persisted by callers.
type Config struct {
	Host      string
	Port      int
	Transport string
	Service   string
	Username  string
	Password  string

	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	OverallTimeout time.Duration
}

// Result reports the authenticated session and every schema returned by the
// accessible metadata views. SchemaTotal always equals len(Schemas).
type Result struct {
	NetworkState        string            `json:"network_state"`
	AuthenticationState string            `json:"authentication_state"`
	ErrorCode           string            `json:"error_code,omitempty"`
	ErrorMessage        string            `json:"error_message,omitempty"`
	ActualUser          string            `json:"actual_user"`
	CurrentSchema       string            `json:"current_schema"`
	DatabaseService     string            `json:"database_service"`
	PDBName             string            `json:"pdb_name"`
	OwnSchema           string            `json:"own_schema"`
	SchemaTotal         int               `json:"schema_total"`
	Schemas             []SchemaCandidate `json:"schemas"`
	SystemPrivileges    []string          `json:"system_privileges"`
	EnabledRoles        []string          `json:"enabled_roles"`
	Capabilities        []Capability      `json:"capabilities"`
	Warnings            []string          `json:"warnings"`
}

type SchemaCandidate struct {
	Name                string   `json:"name"`
	Own                 bool     `json:"own"`
	ObjectCount         int64    `json:"object_count"`
	KnownPrivileges     []string `json:"known_privileges"`
	MigrationCapability string   `json:"migration_capability"`
	CapabilityReason    string   `json:"capability_reason"`
}

type Capability struct {
	View   string `json:"view"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type ProbeError struct {
	Kind    string `json:"kind"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func (e *ProbeError) Error() string { return e.Message }

// Metadata is populated only by the adapter's fixed SELECT statements. It is
// intentionally a row model rather than an arbitrary query result interface.
type Metadata struct {
	ActualUser           string
	CurrentSchema        string
	DatabaseService      string
	PDBName              string
	Users                []UserMetadata
	Objects              []ObjectMetadata
	SystemPrivileges     []string
	EnabledRoles         []string
	SchemaPrivileges     []SchemaPrivilegeMetadata
	RoleSchemaPrivileges []SchemaPrivilegeMetadata
	Errors               map[string]string
	CompletedViews       map[string]bool
}

type UserMetadata struct {
	Name             string
	OracleMaintained string
}

type ObjectMetadata struct {
	Owner string
	Count int64
}

type SchemaPrivilegeMetadata struct {
	Schema    string
	Privilege string
}

// Evidence is a bounded snapshot used to bind recovery decisions to one
// Oracle database and its current schema state.
type Evidence struct {
	DBID               string       `json:"dbid"`
	DatabaseUniqueName string       `json:"database_unique_name"`
	PDBName            string       `json:"pdb_name"`
	Service            string       `json:"service"`
	SessionUser        string       `json:"session_user"`
	CurrentSchema      string       `json:"current_schema"`
	ObjectsSHA256      string       `json:"objects_sha256"`
	History            []HistoryRow `json:"history"`
}

// HistoryRow preserves the physical Flyway history row, including nullable checksums.
type HistoryRow struct {
	Rank          int    `json:"rank"`
	Version       string `json:"version"`
	Description   string `json:"description"`
	Type          string `json:"type"`
	Script        string `json:"script"`
	Checksum      *int64 `json:"checksum"`
	InstalledBy   string `json:"installed_by"`
	InstalledOn   string `json:"installed_on"`
	ExecutionTime int    `json:"execution_time"`
	Success       bool   `json:"success"`
}

// Probe returns a clear unavailable result when the package is used without
// flyway.Runtime. Production callers should call Runtime.ProbeOracle.
func Probe(ctx context.Context, cfg Config) (Result, error) {
	_ = ctx
	if err := Validate(cfg); err != nil {
		return Result{}, err
	}
	return UnavailableResult(), nil
}

func UnavailableResult() Result {
	return unavailableResult("ORACLE_PROBE_UNAVAILABLE", "A verified Flyway JDBC runtime is required for Oracle discovery.")
}

func unavailableResult(code, message string) Result {
	return Result{NetworkState: "unknown", AuthenticationState: "unknown", ErrorCode: code, ErrorMessage: message, Schemas: []SchemaCandidate{}, SystemPrivileges: []string{}, EnabledRoles: []string{}, Capabilities: []Capability{}, Warnings: []string{}}
}

// Validate checks descriptor-safe connection fields and Oracle's documented
// maximum password byte length without trimming or otherwise changing secrets.
func Validate(c Config) error {
	if strings.TrimSpace(c.Host) == "" || len(c.Host) > 253 || c.Port < 1 || c.Port > 65535 || !safeService.MatchString(c.Service) || strings.TrimSpace(c.Username) == "" || len([]byte(c.Username)) > 256 || c.Password == "" || len([]byte(c.Password)) > 4096 {
		return &ProbeError{Kind: "configuration", Message: "Enter valid Oracle connection details and a password of at most 4096 bytes."}
	}
	if ip := net.ParseIP(c.Host); ip == nil {
		if strings.ContainsAny(c.Host, "()/:\\ \t\r\n") {
			return &ProbeError{Kind: "configuration", Message: "Enter a valid Oracle host name or IP address."}
		}
		for _, label := range strings.Split(strings.TrimSuffix(c.Host, "."), ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return &ProbeError{Kind: "configuration", Message: "Enter a valid Oracle host name or IP address."}
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
					return &ProbeError{Kind: "configuration", Message: "Enter a valid Oracle host name or IP address."}
				}
			}
		}
	}
	if !strings.EqualFold(c.Transport, "TCP") && !strings.EqualFold(c.Transport, "TCPS") {
		return &ProbeError{Kind: "configuration", Message: "Oracle transport must be TCP or TCPS."}
	}
	return nil
}

func BoundedTimeouts(cfg Config) (connect, read, overall time.Duration) {
	return bounded(cfg.ConnectTimeout, defaultConnectTimeout, maxConnectTimeout), bounded(cfg.ReadTimeout, defaultReadTimeout, maxReadTimeout), bounded(cfg.OverallTimeout, defaultOverallTimeout, maxOverallTimeout)
}

func bounded(value, fallback, maxValue time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// ConnectionFailure returns sanitized states and guidance for a JDBC error
// code. It never accepts or returns a vendor error message.
func ConnectionFailure(code string) Result {
	kind, network, authentication, message := "connection", "unknown", "unknown", "Oracle connection failed. Check the connection settings and Oracle listener logs."
	switch code {
	case "ORA-01017", "ORA-28000", "ORA-28001", "ORA-28002", "ORA-28009":
		kind, network, authentication = "authentication", "verified", "failed"
		message = "Oracle rejected the credentials or the account is locked or expired. Check the username, password and account status."
	case "ORA-01045":
		kind, network, authentication = "authentication", "verified", "failed"
		message = "Oracle denied login because the account lacks CREATE SESSION. Use an account permitted to connect; discovery does not grant Oracle privileges."
	case "ORA-12514", "ORA-12505", "ORA-12154", "ORA-12261":
		kind, network, authentication = "service", "verified", "unknown"
		message = "The Oracle listener responded, but the configured service or PDB could not be resolved. Check the service name."
	case "ORA-12170", "ORA-12541", "ORA-17868", "ORA-17820", "ORA-12500", "ORA-12516", "ORA-12520", "ORA-12521", "ORA-17002":
		kind, network, authentication = "network", "failed", "unknown"
		message = "Could not connect to the Oracle listener. Check host, port, network policy and listener availability."
	case "TIMEOUT":
		kind, network, authentication = "timeout", "failed", "unknown"
		message = "Oracle connection timed out. Check network access and listener response."
	}
	_ = kind
	return Result{NetworkState: network, AuthenticationState: authentication, ErrorCode: code, ErrorMessage: message, Schemas: []SchemaCandidate{}, SystemPrivileges: []string{}, EnabledRoles: []string{}, Capabilities: []Capability{}, Warnings: []string{}}
}

// ProbeFromMetadata derives user-facing schema and privilege classifications
// from already-read rows. It does not execute SQL or add discovery caps.
func ProbeFromMetadata(cfg Config, metadata Metadata) (Result, error) {
	if err := Validate(cfg); err != nil {
		return Result{}, err
	}
	res := Result{NetworkState: "verified", AuthenticationState: "verified", ActualUser: metadata.ActualUser, CurrentSchema: metadata.CurrentSchema, DatabaseService: metadata.DatabaseService, PDBName: metadata.PDBName, Schemas: []SchemaCandidate{}, SystemPrivileges: []string{}, EnabledRoles: []string{}, Capabilities: []Capability{}, Warnings: []string{}}
	res.OwnSchema = metadata.ActualUser
	schemas := map[string]*SchemaCandidate{}
	if metadata.Errors["PROBE_TIMEOUT"] != "" {
		res.Warnings = append(res.Warnings, "Oracle discovery timed out; completed metadata views are shown and unfinished views remain unknown.")
	}
	if metadata.Errors["PROBE_OUTPUT_LIMIT"] != "" {
		res.Warnings = append(res.Warnings, "Metadata output exceeded 16 MiB; discovery is partial and later metadata views may be missing.")
	}
	if metadata.Errors["PROBE_INCOMPLETE"] != "" {
		res.Warnings = append(res.Warnings, "Oracle metadata output ended before all views completed; completed rows are shown and unfinished views remain unknown.")
	}
	if metadata.Errors["PROBE_SESSION"] != "" {
		res.Warnings = append(res.Warnings, "The authenticated Oracle session ended during metadata discovery; completed rows are shown.")
	}

	if _, failed := metadata.Errors["SESSION_IDENTITY"]; failed {
		res.Warnings = append(res.Warnings, "Oracle authenticated the account, but session identity metadata could not be read.")
		res.Capabilities = append(res.Capabilities, Capability{View: "SESSION_IDENTITY", Status: "unavailable", Detail: "Could not read session identity."})
	}
	if res.ActualUser == "" {
		res.Warnings = append(res.Warnings, "The login user's own schema could not be confirmed because session identity metadata was unavailable.")
	}
	usersError := metadata.Errors["ALL_USERS"]
	maintainedUnavailable := metadata.Errors["ALL_USERS_MAINTAINED"] != ""
	if res.ActualUser != "" {
		addSchema(schemas, res.ActualUser, res.OwnSchema)
	}
	for _, user := range metadata.Users {
		candidate := addSchema(schemas, user.Name, res.OwnSchema)
		if strings.EqualFold(user.OracleMaintained, "Y") {
			candidate.MigrationCapability = "unsupported"
			candidate.CapabilityReason = "Oracle marks this as a maintained account; it is not a supported Flyway target."
		}
	}
	if usersError != "" {
		res.Warnings = append(res.Warnings, "Oracle did not allow complete ALL_USERS metadata discovery; the schema list may be partial.")
		res.Capabilities = append(res.Capabilities, Capability{View: "ALL_USERS", Status: "unavailable", Detail: "The account could not read the visible user list."})
	} else if maintainedUnavailable {
		res.Warnings = append(res.Warnings, "Oracle ALL_USERS does not expose the maintained-account flag; system-account classification may be incomplete.")
		res.Capabilities = append(res.Capabilities, Capability{View: "ALL_USERS", Status: "available", Detail: "Visible usernames were read; maintained-account metadata is unavailable."})
	} else {
		res.Capabilities = append(res.Capabilities, Capability{View: "ALL_USERS", Status: "available", Detail: "Visible usernames and maintained-account flags were read."})
	}
	for _, object := range metadata.Objects {
		candidate := addSchema(schemas, object.Owner, res.OwnSchema)
		candidate.ObjectCount = object.Count
	}
	if metadata.Errors["ALL_OBJECTS"] != "" {
		metadataWarning(&res, "ALL_OBJECTS")
	} else {
		res.Capabilities = append(res.Capabilities, Capability{View: "ALL_OBJECTS", Status: "available", Detail: "Accessible object ownership was read; this does not prove migration rights."})
	}
	privilegesKnown := metadata.Errors["SESSION_PRIVS"] == ""
	if !privilegesKnown {
		metadataWarning(&res, "SESSION_PRIVS")
	} else {
		res.SystemPrivileges = append(res.SystemPrivileges, metadata.SystemPrivileges...)
		res.Capabilities = append(res.Capabilities, Capability{View: "SESSION_PRIVS", Status: "available", Detail: "Currently available system privileges were read."})
	}
	if metadata.Errors["SESSION_ROLES"] != "" {
		metadataWarning(&res, "SESSION_ROLES")
	} else {
		res.EnabledRoles = append(res.EnabledRoles, metadata.EnabledRoles...)
		res.Capabilities = append(res.Capabilities, Capability{View: "SESSION_ROLES", Status: "available", Detail: "Currently enabled roles were read."})
	}
	for _, privilege := range metadata.SchemaPrivileges {
		candidate := addSchema(schemas, privilege.Schema, res.OwnSchema)
		candidate.KnownPrivileges = append(candidate.KnownPrivileges, privilege.Privilege)
	}
	if metadata.Errors["SESSION_SCHEMA_PRIVS"] != "" {
		res.Capabilities = append(res.Capabilities, Capability{View: "SESSION_SCHEMA_PRIVS", Status: "unavailable", Detail: "Schema-level privilege metadata is unavailable on this Oracle version or to this account."})
		res.Warnings = append(res.Warnings, "Schema-level privilege metadata could not be read; cross-schema capability classifications remain uncertain.")
	} else {
		res.Capabilities = append(res.Capabilities, Capability{View: "SESSION_SCHEMA_PRIVS", Status: "available", Detail: "Currently available schema privileges were read."})
	}
	for _, privilege := range metadata.RoleSchemaPrivileges {
		candidate := addSchema(schemas, privilege.Schema, res.OwnSchema)
		candidate.KnownPrivileges = append(candidate.KnownPrivileges, privilege.Privilege)
	}
	if metadata.Errors["ROLE_SCHEMA_PRIVS"] != "" {
		res.Capabilities = append(res.Capabilities, Capability{View: "ROLE_SCHEMA_PRIVS", Status: "unavailable", Detail: "Enabled-role schema privilege metadata is unavailable on this Oracle version or to this account."})
	} else {
		res.Capabilities = append(res.Capabilities, Capability{View: "ROLE_SCHEMA_PRIVS", Status: "available", Detail: "Schema privileges granted to enabled roles were read."})
	}

	for _, candidate := range schemas {
		candidate.KnownPrivileges = append(candidate.KnownPrivileges, systemDDLPrivilegesForSchema(res.SystemPrivileges, candidate.Own)...)
		candidate.KnownPrivileges = uniqueSorted(candidate.KnownPrivileges)
		classifyUnsupportedSchemaName(candidate)
		if candidate.MigrationCapability == "unsupported" {
			continue
		}
		if candidate.Own {
			if hasOwnHistoryTableDDL(res.SystemPrivileges) || hasSchemaPrivilege(candidate.KnownPrivileges, "CREATE TABLE") || hasSchemaPrivilege(candidate.KnownPrivileges, "CREATE ANY TABLE") {
				candidate.MigrationCapability = "potential"
				candidate.CapabilityReason = "Metadata shows a potentially relevant CREATE TABLE privilege for the login user's schema; quotas, object policies and actual DDL behavior were not tested."
			} else if privilegesKnown && metadata.Errors["ALL_OBJECTS"] == "" && candidate.ObjectCount == 0 {
				candidate.MigrationCapability = "unsupported"
				candidate.CapabilityReason = "The account has no known CREATE TABLE privilege and its schema has no visible objects, so Flyway cannot initialize a history table there."
			} else if privilegesKnown {
				candidate.MigrationCapability = "unknown"
				candidate.CapabilityReason = "The account has no known CREATE TABLE privilege; existing objects may support continued migrations, but history initialization and DDL access are uncertain."
			} else {
				candidate.MigrationCapability = "unknown"
				candidate.CapabilityReason = "Session privileges could not be read, so the login user's CREATE TABLE capability is unknown."
			}
		} else if hasCrossSchemaDDL(res.SystemPrivileges) || hasSchemaDDL(candidate.KnownPrivileges) {
			candidate.MigrationCapability = "potential"
			candidate.CapabilityReason = "Metadata shows potentially relevant DDL privileges; actual Flyway DDL authorization was not tested."
		} else {
			candidate.MigrationCapability = "unknown"
			candidate.CapabilityReason = "Schema visibility does not establish DDL authorization; no relevant enabled DDL privilege was confirmed."
		}
	}
	for _, candidate := range schemas {
		res.Schemas = append(res.Schemas, *candidate)
	}
	sort.Slice(res.Schemas, func(i, j int) bool { return res.Schemas[i].Name < res.Schemas[j].Name })
	res.SchemaTotal = len(res.Schemas)
	res.SystemPrivileges = uniqueSorted(res.SystemPrivileges)
	res.EnabledRoles = uniqueSorted(res.EnabledRoles)
	return res, nil
}

func addSchema(schemas map[string]*SchemaCandidate, name, own string) *SchemaCandidate {
	name = strings.TrimSpace(name)
	if candidate := schemas[name]; candidate != nil {
		return candidate
	}
	candidate := &SchemaCandidate{Name: name, Own: name == own, KnownPrivileges: []string{}, MigrationCapability: "unknown", CapabilityReason: "Schema privileges have not been classified."}
	schemas[name] = candidate
	return candidate
}

func classifyUnsupportedSchemaName(candidate *SchemaCandidate) {
	if candidate.MigrationCapability == "unsupported" {
		return
	}
	if _, builtin := builtInSchemas[strings.ToUpper(candidate.Name)]; builtin {
		candidate.MigrationCapability = "unsupported"
		candidate.CapabilityReason = "This is a built-in Oracle account and is not a supported Flyway target."
	} else if !unquotedSchemaIdentifier.MatchString(candidate.Name) {
		candidate.MigrationCapability = "unsupported"
		candidate.CapabilityReason = "Quoted or nonstandard Oracle schema identifiers are not supported as Flyway targets."
	}
}

func metadataWarning(res *Result, view string) {
	res.Capabilities = append(res.Capabilities, Capability{View: view, Status: "unavailable", Detail: "The account could not read this metadata view."})
	res.Warnings = append(res.Warnings, "Oracle metadata view "+view+" could not be read; schema information may be incomplete.")
}

func hasCrossSchemaDDL(privileges []string) bool {
	for _, privilege := range privileges {
		p := strings.ToUpper(privilege)
		if strings.HasPrefix(p, "CREATE ANY ") || strings.HasPrefix(p, "ALTER ANY ") || strings.HasPrefix(p, "DROP ANY ") {
			return true
		}
	}
	return false
}

func systemDDLPrivilegesForSchema(privileges []string, own bool) []string {
	result := []string{}
	for _, privilege := range privileges {
		p := strings.ToUpper(strings.TrimSpace(privilege))
		if own {
			if isOwnDDLPrivilege(p) {
				result = append(result, p)
			}
		} else if strings.HasPrefix(p, "CREATE ANY ") || strings.HasPrefix(p, "ALTER ANY ") || strings.HasPrefix(p, "DROP ANY ") {
			result = append(result, p)
		}
	}
	return uniqueSorted(result)
}

func isOwnDDLPrivilege(privilege string) bool {
	switch privilege {
	case "CREATE TABLE", "CREATE INDEX", "CREATE PROCEDURE", "CREATE SEQUENCE", "CREATE TRIGGER", "CREATE VIEW", "CREATE MATERIALIZED VIEW", "CREATE TYPE", "CREATE SYNONYM", "CREATE CLUSTER",
		"ALTER TABLE", "ALTER INDEX", "ALTER PROCEDURE", "ALTER SEQUENCE", "ALTER TRIGGER", "ALTER VIEW", "ALTER MATERIALIZED VIEW", "ALTER TYPE", "ALTER CLUSTER",
		"DROP TABLE", "DROP INDEX", "DROP PROCEDURE", "DROP SEQUENCE", "DROP TRIGGER", "DROP VIEW", "DROP MATERIALIZED VIEW", "DROP TYPE", "DROP SYNONYM", "DROP CLUSTER":
		return true
	default:
		return strings.HasPrefix(privilege, "CREATE ANY ") || strings.HasPrefix(privilege, "ALTER ANY ") || strings.HasPrefix(privilege, "DROP ANY ")
	}
}

func hasSchemaDDL(privileges []string) bool {
	for _, privilege := range privileges {
		switch strings.ToUpper(privilege) {
		case "CREATE", "ALTER", "DROP", "CREATE TABLE", "ALTER TABLE", "DROP TABLE", "CREATE ANY TABLE", "ALTER ANY TABLE", "DROP ANY TABLE", "CREATE PROCEDURE", "ALTER PROCEDURE", "DROP PROCEDURE", "CREATE ANY PROCEDURE", "ALTER ANY PROCEDURE", "DROP ANY PROCEDURE", "CREATE SEQUENCE", "ALTER SEQUENCE", "DROP SEQUENCE", "CREATE ANY SEQUENCE", "ALTER ANY SEQUENCE", "DROP ANY SEQUENCE", "CREATE TRIGGER", "ALTER TRIGGER", "DROP TRIGGER", "CREATE ANY TRIGGER", "ALTER ANY TRIGGER", "DROP ANY TRIGGER", "CREATE INDEX", "ALTER INDEX", "DROP INDEX", "CREATE ANY INDEX", "ALTER ANY INDEX", "DROP ANY INDEX", "CREATE VIEW", "ALTER VIEW", "DROP VIEW", "CREATE ANY VIEW", "DROP ANY VIEW":
			return true
		}
	}
	return false
}

func hasOwnHistoryTableDDL(privileges []string) bool {
	for _, privilege := range privileges {
		if strings.EqualFold(strings.TrimSpace(privilege), "CREATE TABLE") || strings.EqualFold(strings.TrimSpace(privilege), "CREATE ANY TABLE") {
			return true
		}
	}
	return false
}

func hasSchemaPrivilege(privileges []string, expected string) bool {
	for _, privilege := range privileges {
		if strings.EqualFold(strings.TrimSpace(privilege), expected) {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	if values == nil {
		return []string{}
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToUpper(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
