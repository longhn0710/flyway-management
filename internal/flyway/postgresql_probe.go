package flyway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flyway-manager/internal/postgresql"
)

//go:embed PostgreSQLJdbcProbe.class
var postgresqlProbeClass []byte

const postgresqlProbeClassSHA256 = "520780cefef0b76a2969963ff1dfe451da1b592d22da7bbb196506a7fba93011"
const postgresqlProbeMagic uint32 = 0x50475351
const maxPostgreSQLProbeOutput = 2 << 20

func (r *Runtime) ProbePostgreSQL(ctx context.Context, cfg postgresql.Config) (postgresql.Result, error) {
	if err := postgresql.Validate(cfg); err != nil {
		return postgresql.Result{}, err
	}
	timeout := postgresql.BoundedTimeout(cfg)
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if !r.acquire(probeCtx) {
		result := postgresql.UnavailableResult()
		result.ErrorCode = "TIMEOUT"
		result.ErrorMessage = "The PostgreSQL probe exceeded its time budget."
		return result, nil
	}
	defer r.release()
	root, err := r.distributionRoot()
	if err != nil || r.verifyManifest(root) != nil {
		return postgresql.UnavailableResult(), nil
	}
	javaHome, hasJava := runtimeJavaHome(root)
	if !hasJava || javaHome == "" {
		return postgresql.UnavailableResult(), nil
	}
	jarPath, err := pinnedPostgreSQLJar(root)
	if err != nil {
		return postgresql.UnavailableResult(), nil
	}
	work, err := os.MkdirTemp("", "postgresql-probe-")
	if err != nil {
		return postgresql.UnavailableResult(), nil
	}
	defer os.RemoveAll(work)
	if err := os.Chmod(work, 0700); err != nil {
		return postgresql.UnavailableResult(), nil
	}
	classPath := filepath.Join(work, "PostgreSQLJdbcProbe.class")
	caPath := filepath.Join(work, "ca.pem")
	if err := os.WriteFile(classPath, postgresqlProbeClass, 0600); err != nil {
		return postgresql.UnavailableResult(), nil
	}
	if err := os.WriteFile(caPath, []byte(cfg.CAPEM), 0600); err != nil {
		return postgresql.UnavailableResult(), nil
	}
	payload, err := encodePostgreSQLProbeInput(cfg, caPath, timeout)
	if err != nil {
		return postgresql.Result{}, err
	}
	cmd := exec.CommandContext(probeCtx, filepath.Join(javaHome, "bin", "java"), "-Xms16m", "-Xmx256m", "-cp", work+string(os.PathListSeparator)+jarPath, "PostgreSQLJdbcProbe")
	cmd.Dir = work
	cmd.Env = probeJavaEnv(work, javaHome)
	cmd.Stdin = bytes.NewReader(payload)
	defer clear(payload)
	cmd.Stderr = io.Discard
	configureProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return postgresql.UnavailableResult(), nil
	}
	if err := cmd.Start(); err != nil {
		return postgresql.UnavailableResult(), nil
	}
	meta, state, connected, done, parseErr := readPostgreSQLProbeProtocol(&boundedProbeReader{reader: stdout, remaining: maxPostgreSQLProbeOutput})
	if parseErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if state != "" && !connected {
		return postgresqlConnectionFailure(state), nil
	}
	if !connected {
		if probeCtx.Err() != nil {
			result := postgresql.UnavailableResult()
			result.ErrorCode = "TIMEOUT"
			result.ErrorMessage = "The PostgreSQL probe exceeded its time budget."
			return result, nil
		}
		return postgresql.UnavailableResult(), nil
	}
	if parseErr != nil || !done || waitErr != nil || probeCtx.Err() != nil {
		if ctx.Err() != nil {
			return postgresql.Result{}, ctx.Err()
		}
		result := postgresql.UnavailableResult()
		result.NetworkState = "verified"
		result.AuthenticationState = "unknown"
		result.ErrorCode = "POSTGRESQL_PROBE_INCOMPLETE"
		result.ErrorMessage = "PostgreSQL connected, but bounded identity or schema discovery did not complete."
		if probeCtx.Err() != nil {
			result.ErrorCode = "TIMEOUT"
			result.ErrorMessage = "The PostgreSQL probe exceeded its time budget."
		}
		return result, nil
	}
	result, err := postgresql.ResultFromMetadata(cfg, meta)
	if err != nil {
		return postgresql.Result{}, err
	}
	if state != "" {
		result.Warnings = append(result.Warnings, "Some fixed PostgreSQL metadata could not be read ("+state+").")
	}
	return result, nil
}

func validatePostgreSQLRequest(r Request) error {
	if r.Command == "repair" {
		return errors.New("Flyway repair is disabled for PostgreSQL")
	}
	if r.Service != "" || r.Transport != "TCPS" || r.TLSMode != "verify-full" || !postgresqlName.MatchString(r.DatabaseName) || !postgresqlName.MatchString(r.Schema) || !postgresqlName.MatchString(r.HistorySchema) || !postgresqlName.MatchString(r.HistoryTable) || !postgresqlName.MatchString(r.Username) {
		return errors.New("invalid PostgreSQL target, schema, history, role, or TLS configuration")
	}
	if ip := net.ParseIP(r.Host); ip == nil && !validHostname(r.Host) {
		return errors.New("invalid PostgreSQL host")
	}
	if err := postgresql.Validate(postgresql.Config{Host: r.Host, Port: r.Port, DatabaseName: r.DatabaseName, Username: r.Username, Password: r.Password, CAPEM: r.CAPEM}); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(r.CAPEM))
	if !strings.EqualFold(hex.EncodeToString(sum[:]), r.CAHash) {
		return errors.New("PostgreSQL CA checksum does not match its content")
	}
	if len(r.Files) == 0 && (r.Command == "migrate" || r.Command == "validate") {
		return errors.New("SQL snapshot is empty")
	}
	for name, contents := range r.Files {
		if err := validateRelativePath(name); err != nil {
			return err
		}
		if !strings.HasSuffix(strings.ToLower(name), ".sql") {
			return errors.New("snapshot contains a non-SQL file")
		}
		if r.Command == "migrate" {
			if err := postgresql.ValidateMigrationSQL(contents); err != nil {
				return err
			}
		}
	}
	return nil
}

var postgresqlName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
var postgresqlSQLState = regexp.MustCompile(`^[0-9A-Z]{5}$`)

func makePostgreSQLConfig(r Request, caPath string) string {
	props := postgresqlConfigProperties(r, caPath)
	var b strings.Builder
	b.WriteString("[environments.runtime]\n")
	for _, key := range []string{"url", "user", "password"} {
		b.WriteString(key + " = " + tomlString("!{"+props["flyway."+key]+"}") + "\n")
	}
	b.WriteString("schemas = [" + tomlString(props["flyway.schemas"]) + "]\n\n[flyway]\nenvironment = \"runtime\"\n")
	for _, key := range []string{"defaultSchema", "table"} {
		b.WriteString(key + " = " + tomlString(props["flyway."+key]) + "\n")
	}
	for _, key := range []string{"createSchemas", "cleanDisabled", "validateOnMigrate", "placeholderReplacement", "validateMigrationNaming", "baselineOnMigrate", "outOfOrder"} {
		b.WriteString(key + " = " + props["flyway."+key] + "\n")
	}
	b.WriteString("callbacks = []\nignoreMigrationPatterns = [\"*:pending\"]\n")
	return b.String()
}

func postgresqlConfigProperties(r Request, caPath string) map[string]string {
	host := r.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	urlValue := "jdbc:postgresql://" + host + ":" + strconv.Itoa(r.Port) + "/" + r.DatabaseName + "?sslmode=verify-full&sslrootcert=" + url.QueryEscape(caPath)
	if r.statementBudgetSeconds > 0 {
		// Startup options cover every Flyway JDBC connection, including the
		// history/locking connection. They survive loss of the CLI or Manager.
		budget := strconv.Itoa(r.statementBudgetSeconds * 1000)
		lockBudget := r.statementBudgetSeconds * 1000
		if lockBudget > 5000 {
			lockBudget = 5000
		}
		options := "-c standard_conforming_strings=on -c statement_timeout=" + budget + " -c lock_timeout=" + strconv.Itoa(lockBudget) + " -c transaction_timeout=" + budget + " -c idle_in_transaction_session_timeout=" + budget + " -c idle_session_timeout=" + budget
		urlValue += "&connectTimeout=5&socketTimeout=" + strconv.Itoa(r.statementBudgetSeconds+5) + "&options=" + url.QueryEscape(options)
	}
	if r.ExecutionTag != "" {
		urlValue += "&ApplicationName=" + url.QueryEscape(r.ExecutionTag)
	}
	return map[string]string{
		"flyway.url": urlValue, "flyway.user": r.Username, "flyway.password": r.Password,
		"flyway.schemas": r.Schema, "flyway.defaultSchema": r.HistorySchema, "flyway.table": r.HistoryTable,
		"flyway.createSchemas": "false", "flyway.cleanDisabled": "true", "flyway.validateOnMigrate": "true",
		"flyway.callbacks": "", "flyway.placeholderReplacement": "false", "flyway.ignoreMigrationPatterns": "*:pending",
		"flyway.validateMigrationNaming": "true", "flyway.baselineOnMigrate": "false", "flyway.outOfOrder": "false",
	}
}

func postgresqlConfigurationSnapshot(r Request) map[string]string {
	props := postgresqlConfigProperties(r, "verified-private-ca")
	delete(props, "flyway.password")
	host := r.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	props["flyway.url"] = "jdbc:postgresql://" + host + ":" + strconv.Itoa(r.Port) + "/" + r.DatabaseName + "?sslmode=verify-full&sslrootcert-sha256=" + strings.ToLower(r.CAHash)
	props["engine"] = "postgresql"
	props["databaseName"] = r.DatabaseName
	props["tlsMode"] = r.TLSMode
	props["caSha256"] = strings.ToLower(r.CAHash)
	props["configurationPolicyVersion"] = "2"
	props["executionLifecyclePolicy"] = "postgresql17-bounded-session-v1"
	props["outputType"] = "json"
	props["locations"] = "immutable-run-snapshot"
	return props
}

// PostgreSQLExecutionPolicy freezes runtime-owned bounds and observer identity
// with a queued plan. Changing a process budget must not silently change an
// existing PostgreSQL stream's execution contract.
func (r *Runtime) PostgreSQLExecutionPolicy() map[string]string {
	lock := r.config.TimeoutSeconds * 1000
	if lock > 5000 {
		lock = 5000
	}
	return map[string]string{
		"manager.processTimeoutSeconds":        strconv.Itoa(r.config.TimeoutSeconds),
		"postgresql.statementTimeoutMs":        strconv.Itoa(r.config.TimeoutSeconds * 1000),
		"postgresql.transactionTimeoutMs":      strconv.Itoa(r.config.TimeoutSeconds * 1000),
		"postgresql.idleTimeoutMs":             strconv.Itoa(r.config.TimeoutSeconds * 1000),
		"postgresql.standardConformingStrings": "on",
		"postgresql.lockTimeoutMs":             strconv.Itoa(lock),
		"postgresql.observerSHA256":            postgresqlExecutionGuardClassSHA256,
		"postgresql.observerSessionSHA256":     postgresqlExecutionGuardSessionClassSHA256,
	}
}

func postgresqlConnectionFailure(state string) postgresql.Result {
	result := postgresql.UnavailableResult()
	result.ErrorCode = "POSTGRESQL_" + state
	result.NetworkState = "failed"
	result.AuthenticationState = "unknown"
	result.ErrorMessage = "PostgreSQL could not establish a verified connection. Check the endpoint and TLS trust configuration."
	if strings.HasPrefix(state, "28") {
		result.NetworkState = "verified"
		result.AuthenticationState = "failed"
		result.ErrorMessage = "PostgreSQL rejected the login credentials. Verify the username and password."
	} else if strings.HasPrefix(state, "3D") {
		result.NetworkState = "verified"
		result.ErrorMessage = "The requested PostgreSQL database does not exist or is not accessible."
	}
	return result
}

func pinnedPostgreSQLJar(root string) (string, error) {
	path := filepath.Join(root, "drivers", "postgresql-42.7.14.jar")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("pinned PostgreSQL JDBC 42.7.14 driver is unavailable")
	}
	return path, nil
}

func encodePostgreSQLProbeInput(cfg postgresql.Config, caPath string, timeout time.Duration) ([]byte, error) {
	var b bytes.Buffer
	if err := binary.Write(&b, binary.BigEndian, postgresqlProbeMagic); err != nil {
		return nil, err
	}
	for _, value := range []string{cfg.Host, cfg.DatabaseName, cfg.Username, cfg.Password, caPath} {
		v := []byte(value)
		if len(v) > 1<<16 {
			return nil, errors.New("PostgreSQL probe input exceeds its bounded size")
		}
		if err := binary.Write(&b, binary.BigEndian, uint32(len(v))); err != nil {
			return nil, err
		}
		if _, err := b.Write(v); err != nil {
			return nil, err
		}
	}
	connect := max(1, min(15, int(timeout/time.Second)))
	socket := max(1, min(20, int(timeout/time.Second)))
	query := max(1, min(20, int(timeout/time.Second)))
	for _, v := range []int32{int32(cfg.Port), int32(connect), int32(socket), int32(query)} {
		if err := binary.Write(&b, binary.BigEndian, v); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func readPostgreSQLProbeProtocol(reader io.Reader) (postgresql.Metadata, string, bool, bool, error) {
	meta := postgresql.Metadata{Schemas: []postgresql.SchemaMetadata{}}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 32<<10)
	ready, connected, done, identity := false, false, false, false
	state := ""
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if !ready {
			if line != "READY" {
				return meta, "", false, false, errors.New("invalid PostgreSQL probe protocol")
			}
			ready = true
			continue
		}
		if done {
			return meta, "", connected, false, errors.New("trailing PostgreSQL probe output")
		}
		switch fields[0] {
		case "CONNECTED":
			if len(fields) != 1 || connected {
				return meta, "", connected, false, errors.New("invalid PostgreSQL connection state")
			}
			connected = true
		case "CONNECT_ERROR":
			if len(fields) != 2 || connected || state != "" || !postgresqlSQLState.MatchString(fields[1]) {
				return meta, "", connected, false, errors.New("invalid PostgreSQL connection error")
			}
			state = fields[1]
		case "IDENTITY":
			if len(fields) != 5 || !connected || identity {
				return meta, "", connected, false, errors.New("invalid PostgreSQL identity record")
			}
			values, err := decodePostgreSQLFields(fields[1:])
			if err != nil {
				return meta, "", connected, false, err
			}
			meta.DatabaseName, meta.SessionUser, meta.CurrentUser, meta.ServerVersion = values[0], values[1], values[2], values[3]
			identity = true
		case "SCHEMA":
			if len(fields) != 4 || !connected || !identity || (fields[3] != "0" && fields[3] != "1") {
				return meta, "", connected, false, errors.New("invalid PostgreSQL schema record")
			}
			values, err := decodePostgreSQLFields(fields[1:3])
			if err != nil {
				return meta, "", connected, false, err
			}
			meta.Schemas = append(meta.Schemas, postgresql.SchemaMetadata{Name: values[0], Owner: values[1], Create: fields[3] == "1"})
		case "META_ERROR":
			if len(fields) != 2 || !connected || state != "" || !postgresqlSQLState.MatchString(fields[1]) {
				return meta, "", connected, false, errors.New("invalid PostgreSQL metadata error")
			}
			state = fields[1]
		case "DONE":
			if len(fields) != 1 || done {
				return meta, "", connected, false, errors.New("invalid PostgreSQL completion record")
			}
			done = true
		case "HELPER_ERROR":
			return meta, "", connected, false, errors.New("PostgreSQL helper failed")
		default:
			return meta, "", connected, false, errors.New("unknown PostgreSQL probe record")
		}
	}
	if err := scanner.Err(); err != nil {
		return meta, "", connected, false, err
	}
	if !ready {
		return meta, "", connected, false, errors.New("missing PostgreSQL probe preamble")
	}
	if !connected && state == "" {
		return meta, "", false, false, errors.New("missing PostgreSQL connection state")
	}
	if connected && !identity {
		return meta, state, true, false, errors.New("missing PostgreSQL identity")
	}
	return meta, state, connected, done, nil
}

func decodePostgreSQLFields(fields []string) ([]string, error) {
	values := make([]string, len(fields))
	for i, field := range fields {
		raw, err := base64.StdEncoding.DecodeString(field)
		if err != nil || len(raw) > 4096 {
			return nil, errors.New("invalid PostgreSQL probe field")
		}
		values[i] = string(raw)
	}
	return values, nil
}
