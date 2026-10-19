package flyway

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flyway-manager/internal/oracle"
)

//go:embed OracleEvidenceProbe.class
var oracleEvidenceClass []byte

const oracleEvidenceMagic uint32 = 0x4f524345
const oracleEvidenceClassSHA256 = "66afd8f08b42ca9012ca005fe2e45268c76239eef6af246a9260c8c75958b71c"

var evidenceIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]{0,127}$`)

// OracleEvidence reads the fixed read-only database, object-catalogue and
// Flyway-history snapshot through the pinned Oracle JDBC distribution.
func (r *Runtime) OracleEvidence(ctx context.Context, req Request) (oracle.Evidence, error) {
	if req.Engine != "" && req.Engine != "oracle" {
		return oracle.Evidence{}, errors.New("Oracle evidence requires an Oracle request")
	}
	if !evidenceIdentifier.MatchString(req.HistorySchema) || !evidenceIdentifier.MatchString(req.HistoryTable) || !evidenceIdentifier.MatchString(req.Schema) || !evidenceIdentifier.MatchString(req.Username) {
		return oracle.Evidence{}, errors.New("invalid Oracle evidence identifier")
	}
	cfg := oracle.Config{Host: req.Host, Port: req.Port, Transport: req.Transport, Service: req.Service, Username: req.Username, Password: req.Password}
	if err := oracle.Validate(cfg); err != nil {
		return oracle.Evidence{}, err
	}
	connectTimeout, readTimeout, overallTimeout := oracle.BoundedTimeouts(cfg)
	ctx, cancel := context.WithTimeout(ctx, overallTimeout)
	defer cancel()
	if !r.acquire(ctx) {
		return oracle.Evidence{}, errors.New("Oracle evidence runtime capacity timeout")
	}
	defer r.release()
	root, err := r.distributionRoot()
	if err != nil || r.verifyManifest(root) != nil {
		return oracle.Evidence{}, errors.New("verified Oracle runtime unavailable")
	}
	javaHome, ok := runtimeJavaHome(root)
	if !ok || javaHome == "" {
		return oracle.Evidence{}, errors.New("bundled Java runtime unavailable")
	}
	jar, err := pinnedOracleJar(root)
	if err != nil {
		return oracle.Evidence{}, err
	}
	work, err := os.MkdirTemp("", "oracle-evidence-")
	if err != nil {
		return oracle.Evidence{}, err
	}
	defer os.RemoveAll(work)
	class := filepath.Join(work, "OracleEvidenceProbe.class")
	if err = os.WriteFile(class, oracleEvidenceClass, 0600); err != nil {
		return oracle.Evidence{}, err
	}
	payload, err := encodeOracleEvidenceInput(req, connectTimeout, readTimeout)
	if err != nil {
		return oracle.Evidence{}, err
	}
	defer clear(payload)
	cmd := exec.CommandContext(ctx, filepath.Join(javaHome, "bin", "java"), "-Xms16m", "-Xmx256m", "-cp", work+string(os.PathListSeparator)+jar, "OracleEvidenceProbe")
	cmd.Dir = work
	cmd.Env = probeJavaEnv(work, javaHome)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = io.Discard
	configureProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return oracle.Evidence{}, err
	}
	if err = cmd.Start(); err != nil {
		return oracle.Evidence{}, err
	}
	evidence, parseErr := readOracleEvidenceProtocol(&boundedProbeReader{reader: stdout, remaining: maxOracleProbeOutput})
	if parseErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return oracle.Evidence{}, parseErr
	}
	if waitErr != nil || ctx.Err() != nil {
		return oracle.Evidence{}, errors.New("Oracle evidence query did not complete")
	}
	if !strings.EqualFold(evidence.SessionUser, req.Username) || !strings.EqualFold(evidence.CurrentSchema, req.Schema) {
		return oracle.Evidence{}, errors.New("Oracle evidence session identity does not match the configured account and schema")
	}
	return evidence, nil
}

func encodeOracleEvidenceInput(req Request, connect, read time.Duration) ([]byte, error) {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, oracleEvidenceMagic)
	write := func(s string) error {
		v := []byte(s)
		if len(v) > 1<<16 {
			return errors.New("Oracle evidence input exceeds bounds")
		}
		if err := binary.Write(&b, binary.BigEndian, uint32(len(v))); err != nil {
			return err
		}
		_, err := b.Write(v)
		return err
	}
	for _, s := range []string{req.Host, req.Transport, req.Service, req.Username, req.Password} {
		if err := write(s); err != nil {
			return nil, err
		}
	}
	for _, v := range []int32{int32(req.Port), int32(connect.Milliseconds()), int32(read.Milliseconds()), int32(max(1, int(read.Round(time.Second)/time.Second)))} {
		_ = binary.Write(&b, binary.BigEndian, v)
	}
	for _, s := range []string{req.HistorySchema, req.HistoryTable} {
		if err := write(s); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func readOracleEvidenceProtocol(input io.Reader) (oracle.Evidence, error) {
	e := oracle.Evidence{History: []oracle.HistoryRow{}}
	scan := bufio.NewScanner(input)
	scan.Buffer(make([]byte, 4096), 32<<10)
	ready, connected, done, identity, objects, historyAbsent := false, false, false, false, false, false
	decode := func(s string) (string, error) { b, err := base64.StdEncoding.DecodeString(s); return string(b), err }
	for scan.Scan() {
		f := strings.Split(scan.Text(), "\t")
		if !ready {
			if len(f) != 1 || f[0] != "READY" {
				return e, errors.New("invalid Oracle evidence protocol header")
			}
			ready = true
			continue
		}
		if done {
			return e, errors.New("trailing Oracle evidence protocol data")
		}
		switch f[0] {
		case "CONNECTED":
			if len(f) != 1 || connected {
				return e, errors.New("invalid connection record")
			}
			connected = true
		case "IDENTITY":
			if len(f) != 7 || identity {
				return e, errors.New("invalid identity record")
			}
			v := make([]string, 6)
			for i := range v {
				x, err := decode(f[i+1])
				if err != nil {
					return e, err
				}
				v[i] = x
			}
			if v[0] == "" || v[1] == "" || v[2] == "" || v[3] == "" || v[4] == "" || v[5] == "" {
				return e, errors.New("empty Oracle identity field")
			}
			if _, err := strconv.ParseInt(v[0], 10, 64); err != nil {
				return e, errors.New("invalid Oracle DBID")
			}
			e.DBID, e.DatabaseUniqueName, e.PDBName, e.Service, e.SessionUser, e.CurrentSchema = v[0], v[1], v[2], v[3], v[4], v[5]
			identity = true
		case "OBJECTS":
			if len(f) != 2 || objects || len(f[1]) != 64 {
				return e, errors.New("invalid object fingerprint")
			}
			if _, err := hex.DecodeString(f[1]); err != nil {
				return e, errors.New("invalid object fingerprint")
			}
			e.ObjectsSHA256 = f[1]
			objects = true
		case "HISTORY_ABSENT":
			if len(f) != 1 || historyAbsent || len(e.History) != 0 {
				return e, errors.New("invalid absent-history record")
			}
			historyAbsent = true
		case "HISTORY":
			if len(f) != 11 || historyAbsent {
				return e, errors.New("invalid history record")
			}
			rank, er := strconv.Atoi(f[1])
			if er != nil {
				return e, er
			}
			execTime, er := strconv.Atoi(f[9])
			if er != nil || execTime < 0 {
				return e, errors.New("invalid history execution time")
			}
			row := oracle.HistoryRow{Rank: rank, ExecutionTime: execTime, Success: f[10] == "1"}
			vals := []*string{&row.Version, &row.Description, &row.Type, &row.Script, &row.InstalledBy, &row.InstalledOn}
			indices := []int{2, 3, 4, 5, 7, 8}
			for i, p := range vals {
				x, er := decode(f[indices[i]])
				if er != nil {
					return e, er
				}
				*p = x
			}
			if f[6] != "-" {
				n, er := strconv.ParseInt(f[6], 10, 64)
				if er != nil {
					return e, er
				}
				row.Checksum = &n
			}
			if f[10] != "0" && f[10] != "1" {
				return e, errors.New("invalid history success value")
			}
			e.History = append(e.History, row)
		case "QUERY_ERROR":
			return e, errors.New("Oracle evidence query failed: " + strings.TrimSpace(strings.Join(f[1:], " ")))
		case "DONE":
			if len(f) != 1 || done {
				return e, errors.New("invalid completion record")
			}
			done = true
		default:
			return e, errors.New("unknown Oracle evidence protocol record")
		}
	}
	if err := scan.Err(); err != nil {
		return e, err
	}
	if !ready || !connected || !identity || !objects || !done {
		return e, errors.New("incomplete Oracle evidence response")
	}
	return e, nil
}
