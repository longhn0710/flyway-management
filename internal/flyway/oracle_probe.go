package flyway

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"flyway-manager/internal/oracle"
)

// The helper is compiled from internal/oracle/OracleJdbcProbe.java using
// `javac --release 11 -proc:none -g:none`; its digest is pinned below and checked by
// TestOracleProbeHelperArtifact.
//
//go:embed OracleJdbcProbe.class
var oracleProbeClass []byte

const oracleProbeClassSHA256 = "0ba58c363c440c9432d84643663258d28532ec1bed41aac8801dfdc6df1f27c8"
const oracleProbeMagic uint32 = 0x4f52434c

// ProbeOracle runs the fixed-query metadata helper from this runtime's fully
// verified Flyway distribution. Credentials travel only on stdin.
func (r *Runtime) ProbeOracle(ctx context.Context, cfg oracle.Config) (oracle.Result, error) {
	if err := oracle.Validate(cfg); err != nil {
		return oracle.Result{}, err
	}
	connectTimeout, readTimeout, overallTimeout := oracle.BoundedTimeouts(cfg)
	probeCtx, cancel := context.WithTimeout(ctx, overallTimeout)
	defer cancel()
	if !r.acquire(probeCtx) {
		return oracle.ConnectionFailure("TIMEOUT"), nil
	}
	defer r.release()

	root, err := r.distributionRoot()
	if err != nil || r.verifyManifest(root) != nil {
		return oracle.UnavailableResult(), nil
	}
	javaHome, hasJava := runtimeJavaHome(root)
	if !hasJava || javaHome == "" {
		return oracle.UnavailableResult(), nil
	}
	javaPath := filepath.Join(javaHome, "bin", "java")
	jarPath, err := pinnedOracleJar(root)
	if err != nil {
		return oracle.UnavailableResult(), nil
	}

	work, err := os.MkdirTemp("", "oracle-probe-")
	if err != nil {
		return oracle.UnavailableResult(), nil
	}
	defer os.RemoveAll(work)
	classPath := filepath.Join(work, "OracleJdbcProbe.class")
	if err := os.WriteFile(classPath, oracleProbeClass, 0600); err != nil {
		return oracle.UnavailableResult(), nil
	}

	payload, err := encodeOracleProbeInput(cfg, connectTimeout, readTimeout)
	if err != nil {
		return oracle.Result{}, err
	}
	cmd := exec.CommandContext(probeCtx, javaPath, "-Xms16m", "-Xmx256m", "-cp", work+string(os.PathListSeparator)+jarPath, "OracleJdbcProbe")
	cmd.Dir = work
	cmd.Env = probeJavaEnv(work, javaHome)
	cmd.Stdin = bytes.NewReader(payload)
	defer clear(payload)
	cmd.Stderr = io.Discard
	configureProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return oracle.UnavailableResult(), nil
	}
	if err := cmd.Start(); err != nil {
		return oracle.UnavailableResult(), nil
	}
	metadata, connectionCode, connected, done, parseErr := readOracleProbeProtocol(&boundedProbeReader{reader: stdout, remaining: maxOracleProbeOutput})
	if parseErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if connectionCode != "" && !connected {
		result := oracle.ConnectionFailure(connectionCode)
		if connectionCode == "ORA-01017" && len([]byte(cfg.Password)) > 30 {
			result.ErrorMessage = "Oracle rejected these credentials. Passwords over 30 bytes may be incompatible with the configured pinned JDBC 21 driver on this Oracle release; verify the credentials and the database/JDBC password-length compatibility."
		} else if connectionCode == "ORA-01017" && strings.TrimSpace(cfg.Password) != cfg.Password {
			result.ErrorMessage = "Oracle rejected these credentials. Passwords with leading or trailing whitespace may be incompatible with the configured pinned JDBC 21 driver; verify this credential with the same driver used for migrations. The probe does not trim password bytes."
		}
		return result, nil
	}
	if !connected {
		if probeCtx.Err() != nil {
			return oracle.ConnectionFailure("TIMEOUT"), nil
		}
		return oracle.UnavailableResult(), nil
	}
	if connectionCode != "" && connected {
		metadata.Errors["PROBE_SESSION"] = connectionCode
	}
	if parseErr != nil {
		if errors.Is(parseErr, errOracleProbeOutputLimit) {
			metadata.Errors["PROBE_OUTPUT_LIMIT"] = "LIMIT"
		} else {
			metadata.Errors["PROBE_INCOMPLETE"] = "INCOMPLETE"
		}
	}
	if !done || waitErr != nil || probeCtx.Err() != nil || parseErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return oracle.Result{}, ctx.Err()
		}
		code := "INCOMPLETE"
		if probeCtx.Err() != nil {
			code = "TIMEOUT"
			metadata.Errors["PROBE_TIMEOUT"] = "TIMEOUT"
		}
		markIncompleteViews(&metadata, code)
	}
	return oracle.ProbeFromMetadata(cfg, metadata)
}

func markIncompleteViews(metadata *oracle.Metadata, code string) {
	for _, view := range []string{"SESSION_IDENTITY", "ALL_USERS", "ALL_OBJECTS", "SYSTEM_PRIVS", "ENABLED_ROLES", "SCHEMA_PRIVS", "ROLE_SCHEMA_PRIVS"} {
		if !metadata.CompletedViews[view] && metadata.Errors[metadataErrorView(view)] == "" {
			metadata.Errors[metadataErrorView(view)] = code
		}
	}
}

func pinnedOracleJar(root string) (string, error) {
	matches := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := strings.ToLower(filepath.Base(path))
		if strings.HasPrefix(name, "ojdbc11-") && strings.HasSuffix(name, ".jar") {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil || len(matches) != 1 {
		return "", errors.New("pinned Oracle JDBC driver is unavailable")
	}
	return matches[0], nil
}

func encodeOracleProbeInput(cfg oracle.Config, connectTimeout, readTimeout time.Duration) ([]byte, error) {
	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.BigEndian, oracleProbeMagic); err != nil {
		return nil, err
	}
	for _, value := range []string{cfg.Host, cfg.Transport, cfg.Service, cfg.Username, cfg.Password} {
		bytesValue := []byte(value)
		if len(bytesValue) > 1<<16 {
			return nil, errors.New("Oracle probe input exceeds its bounded size")
		}
		if err := binary.Write(&buffer, binary.BigEndian, uint32(len(bytesValue))); err != nil {
			return nil, err
		}
		if _, err := buffer.Write(bytesValue); err != nil {
			return nil, err
		}
	}
	for _, value := range []int32{int32(cfg.Port), int32(connectTimeout.Milliseconds()), int32(readTimeout.Milliseconds()), int32(max(1, int(readTimeout.Round(time.Second)/time.Second)))} {
		if err := binary.Write(&buffer, binary.BigEndian, value); err != nil {
			return nil, err
		}
	}
	return buffer.Bytes(), nil
}

func probeJavaEnv(work, javaHome string) []string {
	path := filepath.Join(javaHome, "bin") + string(os.PathListSeparator) + "/usr/bin:/bin"
	return []string{"PATH=" + path, "HOME=" + work, "USERPROFILE=" + work, "TMPDIR=" + work, "TEMP=" + work, "TMP=" + work, "LC_ALL=C", "LANG=C", "JAVA_HOME=" + javaHome}
}

func readOracleProbeProtocol(reader io.Reader) (oracle.Metadata, string, bool, bool, error) {
	metadata := oracle.Metadata{Users: []oracle.UserMetadata{}, Objects: []oracle.ObjectMetadata{}, SystemPrivileges: []string{}, EnabledRoles: []string{}, SchemaPrivileges: []oracle.SchemaPrivilegeMetadata{}, RoleSchemaPrivileges: []oracle.SchemaPrivilegeMetadata{}, Errors: map[string]string{}, CompletedViews: map[string]bool{}}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 32<<10)
	ready, connected, done := false, false, false
	connectionCode := ""
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if !ready {
			if line != "READY" {
				return metadata, "", false, false, errors.New("invalid Oracle probe protocol")
			}
			ready = true
			continue
		}
		if done {
			return metadata, "", connected, false, errors.New("trailing Oracle probe output")
		}
		switch fields[0] {
		case "CONNECTED":
			if len(fields) != 1 || connected {
				return metadata, "", connected, false, errors.New("invalid connection state record")
			}
			connected = true
		case "IDENTITY":
			if len(fields) != 5 {
				return metadata, "", connected, false, errors.New("invalid identity record")
			}
			values, err := decodeProbeFields(fields[1:])
			if err != nil {
				return metadata, "", connected, false, err
			}
			metadata.ActualUser, metadata.CurrentSchema, metadata.DatabaseService, metadata.PDBName = values[0], values[1], values[2], values[3]
		case "USER":
			if len(fields) != 3 {
				return metadata, "", connected, false, errors.New("invalid user record")
			}
			values, err := decodeProbeFields(fields[1:])
			if err != nil {
				return metadata, "", connected, false, err
			}
			metadata.Users = append(metadata.Users, oracle.UserMetadata{Name: values[0], OracleMaintained: values[1]})
		case "OBJECT":
			if len(fields) != 3 {
				return metadata, "", connected, false, errors.New("invalid object record")
			}
			values, err := decodeProbeFields(fields[1:2])
			if err != nil {
				return metadata, "", connected, false, err
			}
			count, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil || count < 0 {
				return metadata, "", connected, false, errors.New("invalid object count")
			}
			metadata.Objects = append(metadata.Objects, oracle.ObjectMetadata{Owner: values[0], Count: count})
		case "SYSTEM_PRIVS", "ENABLED_ROLES":
			if len(fields) != 2 {
				return metadata, "", connected, false, errors.New("invalid one-column record")
			}
			values, err := decodeProbeFields(fields[1:])
			if err != nil {
				return metadata, "", connected, false, err
			}
			if fields[0] == "SYSTEM_PRIVS" {
				metadata.SystemPrivileges = append(metadata.SystemPrivileges, values[0])
			} else {
				metadata.EnabledRoles = append(metadata.EnabledRoles, values[0])
			}
		case "SCHEMA_PRIVS", "ROLE_SCHEMA_PRIVS":
			if len(fields) != 3 {
				return metadata, "", connected, false, errors.New("invalid privilege record")
			}
			values, err := decodeProbeFields(fields[1:])
			if err != nil {
				return metadata, "", connected, false, err
			}
			row := oracle.SchemaPrivilegeMetadata{Schema: values[0], Privilege: values[1]}
			if fields[0] == "SCHEMA_PRIVS" {
				metadata.SchemaPrivileges = append(metadata.SchemaPrivileges, row)
			} else {
				metadata.RoleSchemaPrivileges = append(metadata.RoleSchemaPrivileges, row)
			}
		case "META_ERROR":
			if len(fields) != 3 {
				return metadata, "", connected, false, errors.New("invalid metadata error")
			}
			metadata.Errors[metadataErrorView(fields[1])] = fields[2]
		case "CONNECT_ERROR":
			if len(fields) != 2 || connectionCode != "" {
				return metadata, "", connected, false, errors.New("invalid connection error")
			}
			if connected {
				metadata.Errors["PROBE_SESSION"] = fields[1]
			} else {
				connectionCode = fields[1]
			}
		case "DONE":
			if len(fields) != 1 {
				return metadata, "", connected, false, errors.New("invalid completion record")
			}
			done = true
		case "VIEW_DONE":
			if len(fields) != 2 {
				return metadata, "", connected, false, errors.New("invalid view completion record")
			}
			metadata.CompletedViews[fields[1]] = true
		default:
			return metadata, "", connected, false, errors.New("unknown Oracle probe protocol record")
		}
	}
	if err := scanner.Err(); err != nil {
		return metadata, "", connected, false, err
	}
	return metadata, connectionCode, connected, done, nil
}

const maxOracleProbeOutput int64 = 16 << 20

var errOracleProbeOutputLimit = errors.New("Oracle probe output limit exceeded")

type boundedProbeReader struct {
	reader    io.Reader
	remaining int64
}

func (r *boundedProbeReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		var extra [1]byte
		n, err := r.reader.Read(extra[:])
		if n > 0 {
			return 0, errOracleProbeOutputLimit
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func decodeProbeFields(fields []string) ([]string, error) {
	decoded := make([]string, len(fields))
	for index, field := range fields {
		value, err := base64.StdEncoding.DecodeString(field)
		if err != nil {
			return nil, err
		}
		decoded[index] = string(value)
	}
	return decoded, nil
}

func metadataErrorView(protocolView string) string {
	switch protocolView {
	case "SYSTEM_PRIVS":
		return "SESSION_PRIVS"
	case "ENABLED_ROLES":
		return "SESSION_ROLES"
	case "SCHEMA_PRIVS":
		return "SESSION_SCHEMA_PRIVS"
	default:
		return protocolView
	}
}
