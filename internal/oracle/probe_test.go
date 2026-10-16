package oracle

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsDescriptorInjectionAndInvalidInputs(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"host descriptor", func(c *Config) { c.Host = "db)(ADDRESS=(PROTOCOL=TCP" }},
		{"service descriptor", func(c *Config) { c.Service = "pdb)(CONNECT_DATA=(SID=x" }},
		{"unsupported transport", func(c *Config) { c.Transport = "TCPS; DROP TABLE USERS" }},
		{"port out of range", func(c *Config) { c.Port = 65536 }},
		{"missing password", func(c *Config) { c.Password = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.edit(&cfg)
			if err := Validate(cfg); err == nil {
				t.Fatal("invalid connection settings were accepted")
			}
		})
	}
}

func TestValidatePreservesSpacesAndSpecialPasswordBytes(t *testing.T) {
	cfg := validConfig()
	cfg.Password = "  Aa1!@#$%&*+-_:=./?  "
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid surrounding whitespace/special characters rejected: %v", err)
	}
	connectTimeout, readTimeout, overallTimeout := BoundedTimeouts(cfg)
	if connectTimeout != defaultConnectTimeout || readTimeout != defaultReadTimeout || overallTimeout != defaultOverallTimeout {
		t.Fatalf("default timeouts = %v/%v/%v", connectTimeout, readTimeout, overallTimeout)
	}
}

func TestValidateAcceptsTCPAndTCPSHosts(t *testing.T) {
	for _, transport := range []string{"TCP", "TCPS", "tcps"} {
		cfg := validConfig()
		cfg.Transport = transport
		if err := Validate(cfg); err != nil {
			t.Fatalf("validate(%q): %v", transport, err)
		}
	}
	cfg := validConfig()
	cfg.Host = "2001:db8::1"
	if err := Validate(cfg); err != nil {
		t.Fatalf("validate IPv6: %v", err)
	}
}

func TestTimeoutsAreDefaultedAndBounded(t *testing.T) {
	if got := bounded(0, defaultReadTimeout, maxReadTimeout); got != defaultReadTimeout {
		t.Fatalf("default read timeout = %v", got)
	}
	if got := bounded(time.Hour, defaultReadTimeout, maxReadTimeout); got != maxReadTimeout {
		t.Fatalf("bounded read timeout = %v", got)
	}
}

func TestSchemaClassificationNeverClaimsVerified(t *testing.T) {
	if !hasCrossSchemaDDL([]string{"CREATE ANY TABLE"}) {
		t.Fatal("CREATE ANY TABLE should provide potential cross-schema evidence")
	}
	if hasCrossSchemaDDL([]string{"SELECT ANY TABLE", "EXECUTE ANY PROCEDURE"}) {
		t.Fatal("read or execute privileges must not count as DDL evidence")
	}
	if !hasSchemaDDL([]string{"CREATE TABLE"}) {
		t.Fatal("schema-level CREATE TABLE should provide potential evidence")
	}
	if hasOwnHistoryTableDDL([]string{"CREATE SESSION"}) || !hasOwnHistoryTableDDL([]string{"CREATE TABLE"}) || !hasOwnHistoryTableDDL([]string{"CREATE ANY TABLE"}) {
		t.Fatal("own-schema Flyway capability must require CREATE TABLE evidence")
	}
	ownerPrivileges := systemDDLPrivilegesForSchema([]string{"CREATE SESSION", "CREATE TABLE", "CREATE ANY TABLE", "SELECT ANY TABLE"}, true)
	if !hasOwnHistoryTableDDL(ownerPrivileges) || containsString(ownerPrivileges, "CREATE SESSION") || containsString(ownerPrivileges, "SELECT ANY TABLE") {
		t.Fatalf("own-schema privileges should include relevant DDL only: %v", ownerPrivileges)
	}
	crossPrivileges := systemDDLPrivilegesForSchema([]string{"CREATE SESSION", "CREATE TABLE", "CREATE ANY TABLE", "SELECT ANY TABLE"}, false)
	if len(crossPrivileges) != 1 || crossPrivileges[0] != "CREATE ANY TABLE" {
		t.Fatalf("cross-schema privileges should expose scoped ANY DDL only: %v", crossPrivileges)
	}
	candidate := addSchema(map[string]*SchemaCandidate{}, "APP", "APP")
	if !candidate.Own {
		t.Fatal("login user's schema was not identified as its own")
	}
}

func TestQuotedSchemaNamesRemainDistinctAndUnsupported(t *testing.T) {
	schemas := map[string]*SchemaCandidate{}
	quotedLower := addSchema(schemas, "App", "App")
	upper := addSchema(schemas, "APP", "App")
	classifyUnsupportedSchemaName(quotedLower)
	classifyUnsupportedSchemaName(upper)
	if len(schemas) != 2 || !quotedLower.Own || upper.Own {
		t.Fatalf("case-sensitive schema preservation failed: %#v", schemas)
	}
	if quotedLower.MigrationCapability != "unsupported" || upper.MigrationCapability == "unsupported" {
		t.Fatalf("quoted name classification was wrong: %#v %#v", quotedLower, upper)
	}
}

func TestProbeFromMetadataHasNoSchemaCapsAndRetainsPartialRows(t *testing.T) {
	metadata := Metadata{ActualUser: "OWNER", Users: make([]UserMetadata, 205), Errors: map[string]string{"ALL_USERS_MAINTAINED": "ORA-00904", "SESSION_SCHEMA_PRIVS": "ORA-00942", "ROLE_SCHEMA_PRIVS": "ORA-00942", "ALL_OBJECTS": "ORA-00942"}}
	for i := range metadata.Users {
		metadata.Users[i] = UserMetadata{Name: schemaName(i)}
	}
	metadata.Users = append(metadata.Users, UserMetadata{Name: "SYS"}, UserMetadata{Name: "App"}, UserMetadata{Name: "OWNER"})
	metadata.SystemPrivileges = []string{"CREATE TABLE"}
	result, err := ProbeFromMetadata(validConfig(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaTotal != 208 || len(result.Schemas) != 208 {
		t.Fatalf("discovery capped or collapsed visible schemas: total=%d rows=%d", result.SchemaTotal, len(result.Schemas))
	}
	if byName(result.Schemas, "SYS").MigrationCapability != "unsupported" {
		t.Fatal("SYS not rejected when maintained flag is unavailable")
	}
	if byName(result.Schemas, "App").MigrationCapability != "unsupported" {
		t.Fatal("quoted-style schema not marked unsupported")
	}
	if owner := byName(result.Schemas, "OWNER"); owner == nil || owner.MigrationCapability != "potential" {
		t.Fatal("actual user identity or own privilege evidence was lost")
	}
	if !containsString(result.Warnings, "Oracle ALL_USERS does not expose the maintained-account flag; system-account classification may be incomplete.") {
		t.Fatal("maintained metadata fallback warning missing")
	}
	if !containsCapability(result.Capabilities, "SESSION_SCHEMA_PRIVS", "unavailable") || !containsCapability(result.Capabilities, "ALL_OBJECTS", "unavailable") {
		t.Fatal("partial view failures were not preserved")
	}
}

func TestProbeTimeoutRetainsCompletedViews(t *testing.T) {
	metadata := Metadata{ActualUser: "OWNER", Users: []UserMetadata{{Name: "OWNER"}, {Name: "APP1"}, {Name: "APP2"}}, SystemPrivileges: []string{"CREATE TABLE"}, Errors: map[string]string{"PROBE_TIMEOUT": "TIMEOUT", "ALL_OBJECTS": "TIMEOUT", "SESSION_ROLES": "TIMEOUT", "SESSION_SCHEMA_PRIVS": "TIMEOUT", "ROLE_SCHEMA_PRIVS": "TIMEOUT"}, CompletedViews: map[string]bool{"SESSION_IDENTITY": true, "ALL_USERS": true, "SESSION_PRIVS": true}}
	result, err := ProbeFromMetadata(validConfig(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaTotal != 3 || byName(result.Schemas, "OWNER").MigrationCapability != "potential" {
		t.Fatalf("completed user rows were not retained on timeout: %#v", result)
	}
	if !containsString(result.Warnings, "Oracle discovery timed out; completed metadata views are shown and unfinished views remain unknown.") {
		t.Fatal("timeout uncertainty warning missing")
	}
}

func TestOwnSchemaWithoutCreateTableDependsOnVisibleObjects(t *testing.T) {
	base := Metadata{ActualUser: "OWNER", Users: []UserMetadata{{Name: "OWNER"}}, SystemPrivileges: []string{"CREATE SESSION"}, Errors: map[string]string{}, CompletedViews: map[string]bool{}}
	base.Objects = []ObjectMetadata{}
	empty, err := ProbeFromMetadata(validConfig(), base)
	if err != nil || byName(empty.Schemas, "OWNER").MigrationCapability != "unsupported" {
		t.Fatalf("empty CREATE SESSION-only owner should be unsupported: %#v %v", empty, err)
	}
	base.Objects = []ObjectMetadata{{Owner: "OWNER", Count: 3}}
	existing, err := ProbeFromMetadata(validConfig(), base)
	if err != nil || byName(existing.Schemas, "OWNER").MigrationCapability != "unknown" {
		t.Fatalf("existing schema without CREATE TABLE should remain selectable as unknown: %#v %v", existing, err)
	}
	base.Errors["ALL_OBJECTS"] = "TIMEOUT"
	unknown, err := ProbeFromMetadata(validConfig(), base)
	if err != nil || byName(unknown.Schemas, "OWNER").MigrationCapability != "unknown" {
		t.Fatalf("unavailable object metadata must not prove unsupported: %#v %v", unknown, err)
	}
}

func TestConnectionFailureStatesAreSanitized(t *testing.T) {
	for _, test := range []struct{ code, network, auth string }{
		{"ORA-01017", "verified", "failed"}, {"ORA-12514", "verified", "unknown"}, {"ORA-12541", "failed", "unknown"}, {"TIMEOUT", "failed", "unknown"},
	} {
		result := ConnectionFailure(test.code)
		if result.NetworkState != test.network || result.AuthenticationState != test.auth || result.ErrorCode != test.code || result.ErrorMessage == "" {
			t.Fatalf("unexpected failure classification: %#v", result)
		}
	}
}

func TestProbeWithoutVerifiedRuntimeIsExplicitlyUnavailable(t *testing.T) {
	result, err := Probe(nil, validConfig())
	if err != nil || result.ErrorCode != "ORACLE_PROBE_UNAVAILABLE" || result.AuthenticationState != "unknown" {
		t.Fatalf("unexpected no-runtime result: %#v, %v", result, err)
	}
}

func TestRoleSchemaPrivilegeMetadataOnlyIncludesEnabledRoles(t *testing.T) {
	source, err := os.ReadFile("OracleJdbcProbe.java")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "ROLE_SCHEMA_PRIVS WHERE ROLE IN (SELECT ROLE FROM SESSION_ROLES)") {
		t.Fatal("role schema query must be fixed and filtered to enabled roles")
	}
}

func validConfig() Config {
	return Config{Host: "oracle.example.test", Port: 1521, Transport: "TCP", Service: "APP_PDB", Username: "APP_DEPLOY", Password: "transient"}
}

func schemaName(i int) string { return "APP" + fmt.Sprintf("%03d", i) }

func byName(schemas []SchemaCandidate, name string) *SchemaCandidate {
	for i := range schemas {
		if schemas[i].Name == name {
			return &schemas[i]
		}
	}
	return nil
}

func containsCapability(values []Capability, view, status string) bool {
	for _, value := range values {
		if value.View == view && value.Status == status {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestMissingCreateSessionIsActionableAuthenticationFailure(t *testing.T) {
	result := ConnectionFailure("ORA-01045")
	if result.NetworkState != "verified" || result.AuthenticationState != "failed" || !strings.Contains(result.ErrorMessage, "CREATE SESSION") {
		t.Fatal(result)
	}
}
