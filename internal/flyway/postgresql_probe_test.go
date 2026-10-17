package flyway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flyway-manager/internal/postgresql"
)

func postgresTestCA(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "runtime test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func postgresTestRequest(command string) Request {
	ca := "-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----\n"
	sum := sha256.Sum256([]byte(ca))
	return Request{Command: command, Engine: "postgresql", Host: "db.example.test", Port: 5432, DatabaseName: "appdb", Service: "", Transport: "TCPS", Schema: "app", HistorySchema: "app", HistoryTable: "flyway_schema_history", Username: "app_user", Password: "secret", CAPEM: ca, CAHash: hex.EncodeToString(sum[:]), TLSMode: "verify-full"}
}

func TestPostgreSQLRequestValidationFailsClosed(t *testing.T) {
	ca := postgresTestCA(t)
	req := postgresTestRequest("info")
	req.CAPEM = ca
	sum := sha256.Sum256([]byte(ca))
	req.CAHash = hex.EncodeToString(sum[:])
	if err := validateRequest(req); err != nil {
		t.Fatalf("valid PostgreSQL request rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Request){
		"unknown engine":       func(r *Request) { r.Engine = "mysql" },
		"uppercase database":   func(r *Request) { r.DatabaseName = "Appdb" },
		"uppercase role":       func(r *Request) { r.Username = "AppUser" },
		"invalid schema":       func(r *Request) { r.Schema = "app;drop" },
		"missing verify-full":  func(r *Request) { r.TLSMode = "require" },
		"CA checksum mismatch": func(r *Request) { r.CAHash = strings.Repeat("0", 64) },
		"repair denied":        func(r *Request) { r.Command = "repair" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := req
			mutate(&bad)
			if err := validateRequest(bad); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestPostgreSQLRuntimeRejectsNontransactionalMaintenance(t *testing.T) {
	ca := postgresTestCA(t)
	req := postgresTestRequest("migrate")
	req.CAPEM = ca
	sum := sha256.Sum256([]byte(ca))
	req.CAHash = hex.EncodeToString(sum[:])
	for _, sql := range []string{"ALTER TABLE t DETACH PARTITION p CONCURRENTLY", "CLUSTER", "CLUSTER partitioned_table", "ALTER SUBSCRIPTION s REFRESH PUBLICATION"} {
		req.Files = map[string][]byte{"V1__maintenance.sql": []byte(sql)}
		if err := validateRequest(req); err == nil {
			t.Fatalf("runtime admitted unsupported maintenance %q", sql)
		}
	}
	req.Files = map[string][]byte{"V1__ordinary.sql": []byte("CREATE TABLE t(id int)")}
	if err := validateRequest(req); err != nil {
		t.Fatal("ordinary transactional SQL rejected", err)
	}
}

func TestPostgreSQLConfigStagesOnlyTrustPathAndStableSnapshot(t *testing.T) {
	req := postgresTestRequest("info")
	caPath := filepath.Join(t.TempDir(), "private ca.pem")
	config := makePostgreSQLConfig(req, caPath)
	if !strings.Contains(config, "sslmode=verify-full") || !strings.Contains(config, "sslrootcert=") || strings.Contains(config, req.CAPEM) || !strings.Contains(config, "[flyway]") || strings.Contains(config, "[flyway.oracle]") {
		t.Fatal("PostgreSQL config does not contain typed TLS settings or contains raw certificate/Oracle settings")
	}
	snapshot := ConfigurationSnapshot(req)
	for _, value := range snapshot {
		if strings.Contains(value, caPath) || strings.Contains(value, req.CAPEM) || strings.Contains(value, req.Password) {
			t.Fatal("PostgreSQL snapshot contains a secret or temporary path")
		}
	}
	if snapshot["configurationPolicyVersion"] != "2" || snapshot["caSha256"] != req.CAHash || snapshot["tlsMode"] != "verify-full" {
		t.Fatalf("stable profile identity missing from snapshot: %#v", snapshot)
	}
	changed := req
	changed.CAPEM = "changed trust bytes"
	changed.CAHash = "another stable hash"
	if ConfigurationSnapshot(changed)["flyway.url"] == snapshot["flyway.url"] {
		t.Fatal("trust content hash did not affect PostgreSQL configuration identity")
	}
}

func TestPostgreSQLProbeProtocol(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	lines := []string{"READY", "CONNECTED", "IDENTITY\t" + enc("appdb") + "\t" + enc("app_user") + "\t" + enc("app_user") + "\t" + enc("170005"), "SCHEMA\t" + enc("app") + "\t" + enc("app_user") + "\t1", "SCHEMA\t" + enc("readonly") + "\t" + enc("db_owner") + "\t0", "DONE"}
	meta, state, connected, done, err := readPostgreSQLProbeProtocol(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil || state != "" || !connected || !done || meta.DatabaseName != "appdb" || len(meta.Schemas) != 2 {
		t.Fatalf("protocol parse: %#v %q %t %t %v", meta, state, connected, done, err)
	}
	_, state, connected, _, err = readPostgreSQLProbeProtocol(strings.NewReader("READY\nCONNECT_ERROR\t28P01\nDONE\n"))
	if err != nil || state != "28P01" || connected {
		t.Fatalf("connection error parse: state=%q connected=%t err=%v", state, connected, err)
	}
}

func TestPostgreSQLProbeHelperArtifact(t *testing.T) {
	digest := sha256.Sum256(postgresqlProbeClass)
	if hex.EncodeToString(digest[:]) != postgresqlProbeClassSHA256 {
		t.Fatal("embedded PostgreSQL JDBC helper digest differs from source pin")
	}
	if len(postgresqlProbeClass) < 8 || postgresqlProbeClass[6] != 0 || postgresqlProbeClass[7] != 55 {
		t.Fatal("PostgreSQL helper must target Java 11 bytecode")
	}
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable; embedded helper digest remains checked")
	}
	version, err := exec.Command(javac, "-version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "javac 21") {
		t.Skip("exact helper regeneration is pinned to JDK 21")
	}
	work := t.TempDir()
	source := filepath.Join("..", "postgresql", "PostgreSQLJdbcProbe.java")
	cmd := exec.Command(javac, "--release", "11", "-proc:none", "-g:none", "-d", work, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper compile: %v (%s)", err, output)
	}
	generated, err := os.ReadFile(filepath.Join(work, "PostgreSQLJdbcProbe.class"))
	if err != nil {
		t.Fatal(err)
	}
	actual := sha256.Sum256(generated)
	if hex.EncodeToString(actual[:]) != postgresqlProbeClassSHA256 {
		t.Fatal("embedded PostgreSQL helper differs from reproducible build")
	}
}

func TestPostgreSQLProbeLive(t *testing.T) {
	labEnvPath := os.Getenv("FLYWAY_POSTGRES_LAB_ENV")
	runtimePath := os.Getenv("FLYWAY_TEST_RUNTIME_CONFIG")
	if labEnvPath == "" || runtimePath == "" {
		t.Skip("set private PostgreSQL lab env and verified runtime config for live probe")
	}
	for _, path := range []string{labEnvPath, runtimePath} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			t.Fatal("private PostgreSQL lab or runtime config unavailable or permissions too broad")
		}
	}
	labBytes, err := os.ReadFile(labEnvPath)
	if err != nil {
		t.Fatal("cannot read private PostgreSQL lab environment")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(labBytes), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			if decoded, e := strconv.Unquote(value); e == nil {
				value = decoded
			}
		}
		if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	port, err := strconv.Atoi(values["PGPORT"])
	if err != nil || values["PGHOST"] == "" || values["PGDATABASE"] == "" || values["PGUSER"] == "" || values["PGPASSWORD"] == "" || values["PGSSLROOTCERT"] == "" || values["PGSSLMODE"] != "verify-full" {
		t.Fatal("private PostgreSQL lab config is incomplete or does not require verify-full")
	}
	runtimeBytes, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal("cannot read private runtime config")
	}
	var runtimeConfig struct {
		Flyway Config `json:"flyway"`
	}
	if json.Unmarshal(runtimeBytes, &runtimeConfig) != nil {
		t.Fatal("private runtime config is invalid")
	}
	caInfo, err := os.Stat(values["PGSSLROOTCERT"])
	if err != nil || !caInfo.Mode().IsRegular() {
		t.Fatal("PostgreSQL lab CA unavailable")
	}
	ca, err := os.ReadFile(values["PGSSLROOTCERT"])
	if err != nil {
		t.Fatal("cannot read PostgreSQL lab CA")
	}
	runner := New(runtimeConfig.Flyway, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	probe := func(host string, probePort int, database, username, password, trust string) postgresql.Result {
		t.Helper()
		result, err := runner.ProbePostgreSQL(ctx, postgresql.Config{Host: host, Port: probePort, DatabaseName: database, Username: username, Password: password, CAPEM: trust, OverallTimeout: 12 * time.Second})
		if err != nil {
			t.Fatal("PostgreSQL probe returned an unexpected transport error")
		}
		return result
	}
	result := probe(values["PGHOST"], port, values["PGDATABASE"], values["PGUSER"], values["PGPASSWORD"], string(ca))
	if err != nil || result.AuthenticationState != "verified" || result.DatabaseName != values["PGDATABASE"] || result.SessionUser != values["PGUSER"] || !strings.HasPrefix(result.ServerVersion, "17") || result.TLSMode != "verify-full" || result.ErrorCode != "" {
		t.Fatalf("PostgreSQL lab identity/TLS probe failed: network=%s auth=%s code=%s version=%s", result.NetworkState, result.AuthenticationState, result.ErrorCode, result.ServerVersion)
	}
	if len(result.Schemas) == 0 {
		t.Fatal("PostgreSQL lab returned no existing schemas with USAGE")
	}
	schema := result.Schemas[0].Name
	caHash := sha256.Sum256(ca)
	info := Request{Command: "info", Engine: "postgresql", Host: values["PGHOST"], Port: port, Transport: "TCPS", DatabaseName: values["PGDATABASE"], Schema: schema, HistorySchema: schema, HistoryTable: "flyway_schema_history", Username: values["PGUSER"], Password: values["PGPASSWORD"], CAPEM: string(ca), CAHash: hex.EncodeToString(caHash[:]), TLSMode: "verify-full"}
	cliResult := runner.Execute(ctx, info)
	if !cliResult.Success {
		t.Fatalf("PostgreSQL Flyway Info failed: exit=%d timeout=%t", cliResult.ExitCode, cliResult.TimedOut)
	}

	t.Run("wrong password", func(t *testing.T) {
		got := probe(values["PGHOST"], port, values["PGDATABASE"], values["PGUSER"], values["PGPASSWORD"]+"-wrong", string(ca))
		if got.NetworkState != "verified" || got.AuthenticationState != "failed" || got.ErrorCode != "POSTGRESQL_28P01" {
			t.Fatalf("wrong password classification: network=%s auth=%s code=%s", got.NetworkState, got.AuthenticationState, got.ErrorCode)
		}
	})
	t.Run("missing database", func(t *testing.T) {
		database := "fm_missing_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		got := probe(values["PGHOST"], port, database, values["PGUSER"], values["PGPASSWORD"], string(ca))
		if got.NetworkState != "verified" || got.ErrorCode != "POSTGRESQL_3D000" {
			t.Fatalf("missing database classification: network=%s auth=%s code=%s", got.NetworkState, got.AuthenticationState, got.ErrorCode)
		}
	})
	t.Run("unavailable listener", func(t *testing.T) {
		got := probe(values["PGHOST"], 1, values["PGDATABASE"], values["PGUSER"], values["PGPASSWORD"], string(ca))
		if got.NetworkState != "failed" || got.AuthenticationState != "unknown" {
			t.Fatalf("unavailable listener classification: network=%s auth=%s code=%s", got.NetworkState, got.AuthenticationState, got.ErrorCode)
		}
	})
	t.Run("wrong TLS hostname", func(t *testing.T) {
		got := probe("127.0.0.1", port, values["PGDATABASE"], values["PGUSER"], values["PGPASSWORD"], string(ca))
		if got.NetworkState != "failed" || got.AuthenticationState != "unknown" {
			t.Fatalf("wrong hostname passed verify-full: network=%s auth=%s code=%s", got.NetworkState, got.AuthenticationState, got.ErrorCode)
		}
	})
	t.Run("untrusted CA", func(t *testing.T) {
		got := probe(values["PGHOST"], port, values["PGDATABASE"], values["PGUSER"], values["PGPASSWORD"], postgresTestCA(t))
		if got.NetworkState != "failed" || got.AuthenticationState != "unknown" {
			t.Fatalf("untrusted CA passed verify-full: network=%s auth=%s code=%s", got.NetworkState, got.AuthenticationState, got.ErrorCode)
		}
	})
	t.Run("read-only schema classification", func(t *testing.T) {
		username, password := values["PGREADONLY_USER"], values["PGREADONLY_PASSWORD"]
		if username == "" || password == "" {
			t.Skip("private read-only PostgreSQL credentials are not configured")
		}
		got := probe(values["PGHOST"], port, values["PGDATABASE"], username, password, string(ca))
		if got.AuthenticationState != "verified" || got.ErrorCode != "" {
			t.Fatalf("read-only identity rejected: auth=%s code=%s", got.AuthenticationState, got.ErrorCode)
		}
		foundReadOnly := false
		for _, schema := range got.Schemas {
			if schema.MigrationCapability == "unknown" && !schema.Own {
				foundReadOnly = true
			}
		}
		if !foundReadOnly {
			t.Fatal("read-only role had no visible schema classified as unknown")
		}
	})
}
