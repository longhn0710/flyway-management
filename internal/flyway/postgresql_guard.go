package flyway

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"flyway-manager/internal/postgresql"
)

//go:embed PostgreSQLExecutionGuard.class
var postgresqlExecutionGuardClass []byte

//go:embed PostgreSQLExecutionGuard$Session.class
var postgresqlExecutionGuardSessionClass []byte

// Keep this digest synchronized with a --release 11 build of the Java source.
const postgresqlExecutionGuardClassSHA256 = "824368ecf6417f88162fe555dc3ada722908fdd2166bf35f0067a8871285edad"
const postgresqlExecutionGuardSessionClassSHA256 = "76f022d754db51f926581825b57d146eaef11de901d601286953ab40e0345cfa"

var postgresqlExecutionApplication = regexp.MustCompile(`^fwm_[A-Za-z0-9_-]{16,58}$`)

type postgresqlGuardSession struct {
	PID           int    `json:"pid"`
	BackendStart  string `json:"backend_start"`
	State         string `json:"state"`
	XactStart     string `json:"xact_start"`
	QueryStart    string `json:"query_start"`
	WaitEventType string `json:"wait_event_type"`
	WaitEvent     string `json:"wait_event"`
}

// PostgreSQLExecutionEvidence is deliberately metadata-only: statement text,
// credentials, and JDBC URLs are never retained or returned.
type PostgreSQLExecutionEvidence struct {
	ApplicationName string                   `json:"application_name"`
	Sessions        []postgresqlGuardSession `json:"sessions"`
	Outcome         string                   `json:"outcome"`
	Gone            bool                     `json:"gone"`
	CleanupVerified bool                     `json:"cleanup_verified"`
}

type postgresqlGuard struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      *bufio.Scanner
	app         string
	finished    bool
	journalPath string
}

// startPostgreSQLGuard establishes a dedicated verified JDBC control session
// before Flyway starts. The target session must use the same application tag.
func (r *Runtime) startPostgreSQLGuard(ctx context.Context, req Request, root, work string) (*postgresqlGuard, error) {
	if !postgresqlExecutionApplication.MatchString(req.ExecutionTag) {
		return nil, errors.New("invalid PostgreSQL execution tag")
	}
	javaHome, ok := runtimeJavaHome(root)
	if !ok || javaHome == "" {
		return nil, errors.New("verified Java runtime unavailable for PostgreSQL execution guard")
	}
	jar, err := pinnedPostgreSQLJar(root)
	if err != nil {
		return nil, err
	}
	classPath := filepath.Join(work, "PostgreSQLExecutionGuard.class")
	if err := os.WriteFile(classPath, postgresqlExecutionGuardClass, 0600); err != nil {
		return nil, errors.New("unable to stage PostgreSQL execution guard")
	}
	if err := os.WriteFile(filepath.Join(work, "PostgreSQLExecutionGuard$Session.class"), postgresqlExecutionGuardSessionClass, 0600); err != nil {
		return nil, errors.New("unable to stage PostgreSQL execution guard")
	}
	caPath := filepath.Join(work, "ca.pem")
	input, err := encodePostgreSQLProbeInput(postgresql.Config{Host: req.Host, Port: req.Port, DatabaseName: req.DatabaseName, Username: req.Username, Password: req.Password, CAPEM: req.CAPEM}, caPath, 15*time.Second)
	if err != nil {
		return nil, err
	}
	var tail bytes.Buffer
	_ = binary.Write(&tail, binary.BigEndian, uint32(len(req.ExecutionTag)))
	tail.WriteString(req.ExecutionTag)
	budget := req.statementBudgetSeconds
	if budget <= 0 || budget > maxTimeoutSeconds {
		return nil, errors.New("invalid PostgreSQL execution guard lifetime")
	}
	_ = binary.Write(&tail, binary.BigEndian, int32(budget))
	evidenceDir := filepath.Join(r.dataDir, "postgresql-evidence")
	if err := os.MkdirAll(evidenceDir, 0700); err != nil {
		return nil, errors.New("unable to prepare PostgreSQL evidence directory")
	}
	journalPath := filepath.Join(evidenceDir, req.ExecutionTag+".jsonl")
	journal, err := os.OpenFile(journalPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("unable to reserve immutable PostgreSQL execution journal")
	}
	if err := journal.Close(); err != nil {
		return nil, err
	}
	// Persist the reserved filename before the CLI may produce database effects.
	directory, err := os.Open(evidenceDir)
	if err != nil {
		return nil, err
	}
	err = directory.Sync()
	_ = directory.Close()
	if err != nil {
		return nil, errors.New("unable to durably reserve PostgreSQL execution journal")
	}
	_ = binary.Write(&tail, binary.BigEndian, uint32(len(journalPath)))
	tail.WriteString(journalPath)
	input = append(input, tail.Bytes()...)
	defer clear(input)
	// A separate context keeps cleanup alive after the migration's context ends.
	// CommandContext is required by the process-group Cancel hook; the Java
	// watchdog and Finish/abort bound and reap this independent child.
	cmd := exec.CommandContext(context.Background(), filepath.Join(javaHome, "bin", "java"), "-Xms16m", "-Xmx128m", "-cp", work+string(os.PathListSeparator)+jar, "PostgreSQLExecutionGuard")
	cmd.Dir = work
	cmd.Env = probeJavaEnv(work, javaHome)
	configureProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.New("unable to open guard control channel")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, errors.New("unable to open guard evidence channel")
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, errors.New("unable to start PostgreSQL execution guard")
	}
	g := &postgresqlGuard{cmd: cmd, stdin: stdin, stdout: bufio.NewScanner(stdout), app: req.ExecutionTag, journalPath: journalPath}
	g.stdout.Buffer(make([]byte, 4096), 1<<20)
	ready := make(chan bool, 1)
	go func() {
		if _, err := stdin.Write(input); err != nil {
			ready <- false
			return
		}
		ready <- g.stdout.Scan() && g.stdout.Text() == "READY"
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case ok := <-ready:
		if !ok {
			g.abort()
			return nil, errors.New("PostgreSQL execution guard identity verification failed")
		}
	case <-ctx.Done():
		g.abort()
		<-ready // Join the writer before clearing its credential input.
		return nil, ctx.Err()
	case <-timer.C:
		g.abort()
		<-ready
		return nil, errors.New("PostgreSQL execution guard startup timed out")
	}
	return g, nil
}

// Finish observes backend disappearance after the local process group exits.
// The tag is correlation metadata and never authorizes backend termination.
func (g *postgresqlGuard) Finish(cancel bool) (json.RawMessage, bool) {
	if g == nil || g.finished {
		return nil, false
	}
	g.finished = true
	command := byte('S')
	if cancel {
		command = 'C'
	}
	_, _ = g.stdin.Write([]byte{command})
	_ = g.stdin.Close()
	type answer struct {
		line string
		ok   bool
	}
	ch := make(chan answer, 1)
	go func() {
		if g.stdout.Scan() {
			ch <- answer{line: g.stdout.Text(), ok: true}
		} else {
			ch <- answer{}
		}
	}()
	var evidence PostgreSQLExecutionEvidence
	var verified bool
	select {
	case a := <-ch:
		if a.ok {
			evidence, verified = parsePostgreSQLGuardEvidence(a.line, g.app)
		}
	case <-time.After(6 * time.Second):
	}
	if g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
	}
	_ = g.cmd.Wait()
	if !verified {
		return nil, false
	}
	b, err := json.Marshal(evidence)
	if err != nil {
		return nil, false
	}
	// A watchdog result from before the CLI finished cannot acknowledge the
	// caller's completion request. Retain it while failing cleanup verification.
	expectedOutcome := "success_gone"
	if cancel {
		expectedOutcome = "cancelled_gone"
	}
	return b, evidence.Gone && evidence.CleanupVerified && evidence.Outcome == expectedOutcome
}

func (g *postgresqlGuard) abort() {
	if g == nil || g.cmd == nil || g.finished {
		return
	}
	if g.stdin != nil {
		_ = g.stdin.Close()
	}
	if g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
	}
	_ = g.cmd.Wait()
}

func (g *postgresqlGuard) recordCLI(pid int) error {
	group, err := processGroupID(pid)
	if err != nil || group != pid {
		return errors.New("unable to verify the private CLI process group")
	}
	b, _ := json.Marshal(map[string]any{"event": "cli_started", "application_name": g.app, "cli_pid": pid, "cli_process_group_id": group, "at": time.Now().UTC().Format(time.RFC3339Nano)})
	f, err := os.OpenFile(g.journalPath, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// ReadPostgreSQLLifecycle returns only bounded metadata from a server-generated
// tag. It is used by restart reconciliation; it never releases a held stream.
func (r *Runtime) ReadPostgreSQLLifecycle(tag string) (json.RawMessage, error) {
	if !postgresqlExecutionApplication.MatchString(tag) {
		return nil, errors.New("invalid PostgreSQL evidence tag")
	}
	f, err := os.Open(filepath.Join(r.dataDir, "postgresql-evidence", tag+".jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxOutput {
		return nil, errors.New("PostgreSQL journal is invalid or exceeds evidence budget")
	}
	lines := bufio.NewScanner(io.LimitReader(f, maxOutput+1))
	lines.Buffer(make([]byte, 4096), maxOutput)
	events := []json.RawMessage{}
	for lines.Scan() {
		line := bytes.Clone(lines.Bytes())
		var event struct {
			ApplicationName string `json:"application_name"`
		}
		if len(events) >= 128 || json.Unmarshal(line, &event) != nil || event.ApplicationName != tag {
			return nil, errors.New("PostgreSQL journal identity or format is invalid")
		}
		events = append(events, json.RawMessage(line))
	}
	if lines.Err() != nil {
		return nil, errors.New("PostgreSQL journal could not be read completely")
	}
	return json.Marshal(map[string]any{"application_name": tag, "events": events})
}

func parsePostgreSQLGuardEvidence(line, app string) (PostgreSQLExecutionEvidence, bool) {
	fields := strings.Split(line, "\t")
	if len(fields) != 6 || fields[0] != "DONE" || fields[4] != app {
		return PostgreSQLExecutionEvidence{}, false
	}
	switch fields[1] {
	case "success_gone", "cancelled_gone", "cancelled_unknown", "unknown":
	default:
		return PostgreSQLExecutionEvidence{}, false
	}
	if (fields[2] != "true" && fields[2] != "false") || (fields[3] != "true" && fields[3] != "false") {
		return PostgreSQLExecutionEvidence{}, false
	}
	raw, err := base64.StdEncoding.DecodeString(fields[5])
	if err != nil {
		return PostgreSQLExecutionEvidence{}, false
	}
	var sessions []postgresqlGuardSession
	if err = json.Unmarshal(raw, &sessions); err != nil || len(sessions) > 64 {
		return PostgreSQLExecutionEvidence{}, false
	}
	if sessions == nil {
		return PostgreSQLExecutionEvidence{}, false
	}
	for _, s := range sessions {
		if s.PID <= 0 || s.BackendStart == "" {
			return PostgreSQLExecutionEvidence{}, false
		}
	}
	gone, cleanup := fields[2] == "true", fields[3] == "true"
	if gone != cleanup || ((fields[1] == "success_gone" || fields[1] == "cancelled_gone") != gone) {
		return PostgreSQLExecutionEvidence{}, false
	}
	return PostgreSQLExecutionEvidence{ApplicationName: app, Sessions: sessions, Outcome: fields[1], Gone: gone, CleanupVerified: cleanup}, true
}
