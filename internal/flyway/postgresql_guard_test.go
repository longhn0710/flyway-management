package flyway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLJournalRecordsPrivateCLIProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("qualified PostgreSQL lifecycle uses Linux process groups")
	}
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exec sleep 30")
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Cancel(); _ = cmd.Wait() }()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	g := postgresqlGuard{app: "fwm_0123456789abcdef", journalPath: path}
	if err := g.recordCLI(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		PID   int `json:"cli_pid"`
		Group int `json:"cli_process_group_id"`
	}
	if err := json.Unmarshal(contents, &event); err != nil || event.PID != cmd.Process.Pid || event.Group != cmd.Process.Pid {
		t.Fatalf("CLI process group correlation was not retained: %s (%v)", contents, err)
	}
	// A process in the parent's group must never be accepted as this private CLI.
	other := exec.Command("sh", "-c", "exec sleep 30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	if err := g.recordCLI(other.Process.Pid); err == nil {
		t.Fatal("a process in another group was accepted as a private CLI group")
	}
}

func TestPostgreSQLGuardApplicationTag(t *testing.T) {
	for _, tag := range []string{"fwm_0123456789abcdef", "fwm_0123456789abcdef-0123456789abcdef"} {
		if !postgresqlExecutionApplication.MatchString(tag) {
			t.Fatalf("valid execution tag rejected: %q", tag)
		}
	}
	for _, tag := range []string{"", "flyway_0123456789abcdef", "fwm_short", "fwm_0123456789abcdef;select", "fwm_0123456789abcdef\n"} {
		if postgresqlExecutionApplication.MatchString(tag) {
			t.Fatalf("invalid execution tag accepted: %q", tag)
		}
	}
}

func TestPostgreSQLExecutionBoundsAreRuntimeOwnedAndFrozen(t *testing.T) {
	req := postgresTestRequest("migrate")
	req.ExecutionTag = "fwm_0123456789abcdef"
	req.statementBudgetSeconds = 12
	u, err := url.Parse(postgresqlConfigProperties(req, "/private/ca.pem")["flyway.url"])
	if err != nil {
		t.Fatal(err)
	}
	options := u.Query().Get("options")
	for _, setting := range []string{"standard_conforming_strings=on", "statement_timeout=12000", "transaction_timeout=12000", "lock_timeout=5000", "idle_in_transaction_session_timeout=12000", "idle_session_timeout=12000"} {
		if !strings.Contains(options, setting) {
			t.Fatal("missing database-side bound", setting)
		}
	}
	if u.Query().Get("ApplicationName") != req.ExecutionTag || u.Query().Get("socketTimeout") != "17" {
		t.Fatal("missing bounded correlated JDBC settings")
	}
	a := New(Config{TimeoutSeconds: 12}, t.TempDir()).PostgreSQLExecutionPolicy()
	b := New(Config{TimeoutSeconds: 13}, t.TempDir()).PostgreSQLExecutionPolicy()
	if a["postgresql.transactionTimeoutMs"] == b["postgresql.transactionTimeoutMs"] || a["postgresql.observerSHA256"] == "" {
		t.Fatal("runtime budget/observer identity was not frozen")
	}
}

func TestPostgreSQLJournalRejectsInvalidIdentityAndIncompleteEvidence(t *testing.T) {
	r := New(Config{}, t.TempDir())
	dir := filepath.Join(r.dataDir, "postgresql-evidence")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	tag := "fwm_0123456789abcdef"
	if _, err := r.ReadPostgreSQLLifecycle("../outside"); err == nil {
		t.Fatal("unsafe journal tag accepted")
	}
	for _, contents := range []string{`{"application_name":"another"}`, `{"application_name":`, strings.Repeat("x", maxOutput+1)} {
		if err := os.WriteFile(filepath.Join(dir, tag+".jsonl"), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ReadPostgreSQLLifecycle(tag); err == nil {
			t.Fatal("invalid journal accepted")
		}
	}
}

func TestPostgreSQLGuardEvidenceProtocol(t *testing.T) {
	app := "fwm_0123456789abcdef"
	sessions := `[{"pid":123,"backend_start":"2026-10-17T01:02:03Z","state":"active","xact_start":"2026-10-17T01:02:04Z","query_start":"2026-10-17T01:02:05Z","wait_event_type":"","wait_event":""}]`
	line := "DONE\tcancelled_gone\ttrue\ttrue\t" + app + "\t" + base64.StdEncoding.EncodeToString([]byte(sessions))
	evidence, verified := parsePostgreSQLGuardEvidence(line, app)
	if !verified || !evidence.Gone || !evidence.CleanupVerified || len(evidence.Sessions) != 1 || evidence.Sessions[0].PID != 123 {
		t.Fatalf("valid guard evidence rejected: %#v", evidence)
	}
	activeAndIdle := `[{"pid":123,"backend_start":"2026-10-17T01:02:03Z","state":"active","xact_start":"2026-10-17T01:02:04Z","query_start":"2026-10-17T01:02:05Z","wait_event_type":"","wait_event":""},{"pid":123,"backend_start":"2026-10-17T01:02:03Z","state":"idle","xact_start":"","query_start":"","wait_event_type":"Client","wait_event":"ClientRead"}]`
	unknownLine := "DONE\tcancelled_unknown\tfalse\tfalse\t" + app + "\t" + base64.StdEncoding.EncodeToString([]byte(activeAndIdle))
	unknown, ok := parsePostgreSQLGuardEvidence(unknownLine, app)
	if !ok || unknown.Gone || unknown.CleanupVerified || len(unknown.Sessions) != 2 || unknown.Sessions[0].State != "active" {
		t.Fatalf("bounded lifecycle snapshots were not preserved: %#v", unknown)
	}
	badSessions := strings.Replace(sessions, `"pid":123`, `"pid":0`, 1)
	for _, bad := range []string{line + "\textra", strings.Replace(line, app, "fwm_abcdefghijklmnop", 1), strings.Replace(line, "true\ttrue", "maybe\ttrue", 1), "DONE\tcancelled_gone\ttrue\ttrue\t" + app + "\t" + base64.StdEncoding.EncodeToString([]byte(badSessions)), strings.Replace(unknownLine, "false\tfalse", "true\ttrue", 1)} {
		if _, ok := parsePostgreSQLGuardEvidence(bad, app); ok {
			t.Fatalf("invalid guard evidence accepted: %q", bad)
		}
	}
}

func TestPostgreSQLGuardHelperArtifact(t *testing.T) {
	check := func(class []byte, want string) {
		sum := sha256.Sum256(class)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("embedded PostgreSQL execution guard digest differs from source pin")
		}
	}
	check(postgresqlExecutionGuardClass, postgresqlExecutionGuardClassSHA256)
	check(postgresqlExecutionGuardSessionClass, postgresqlExecutionGuardSessionClassSHA256)
	if len(postgresqlExecutionGuardClass) < 8 || postgresqlExecutionGuardClass[6] != 0 || postgresqlExecutionGuardClass[7] != 55 {
		t.Fatal("PostgreSQL execution guard must target Java 11 bytecode")
	}
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable; embedded guard digests remain checked")
	}
	version, err := exec.Command(javac, "-version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "javac 21") {
		t.Skip("exact helper regeneration is pinned to JDK 21")
	}
	work := t.TempDir()
	cmd := exec.Command(javac, "--release", "11", "-proc:none", "-g:none", "-d", work, filepath.Join("..", "postgresql", "PostgreSQLExecutionGuard.java"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("guard helper compile: %v (%s)", err, output)
	}
	for name, want := range map[string]string{"PostgreSQLExecutionGuard.class": postgresqlExecutionGuardClassSHA256, "PostgreSQLExecutionGuard$Session.class": postgresqlExecutionGuardSessionClassSHA256} {
		generated, err := os.ReadFile(filepath.Join(work, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(generated)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatalf("embedded helper %s differs from reproducible build", name)
		}
	}
}

// Opt-in control-plane smoke check; no migration or session termination occurs.
func TestPostgreSQLGuardLiveEmpty(t *testing.T) {
	if os.Getenv("FWM_POSTGRES_MIGRATION_LAB") != "1" {
		t.Skip("requires named disposable local lab")
	}
	ca, err := os.ReadFile(os.Getenv("PGSSLROOTCERT"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(os.Getenv("PGPORT"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(Config{Executable: os.Getenv("FWM_LAB_FLYWAY"), Version: "13.10.3", SHA256: "a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c", TimeoutSeconds: 12}, t.TempDir())
	root, err := r.distributionRoot()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err = os.WriteFile(filepath.Join(work, "ca.pem"), ca, 0600); err != nil {
		t.Fatal(err)
	}
	req := Request{Engine: "postgresql", ExecutionTag: "fwm_guard_empty_0123456789abcdef", Host: os.Getenv("PGHOST"), Port: port, DatabaseName: os.Getenv("PGDATABASE"), Username: os.Getenv("PGUSER"), Password: os.Getenv("PGPASSWORD"), CAPEM: string(ca), statementBudgetSeconds: 12}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	g, err := r.startPostgreSQLGuard(ctx, req, root, work)
	if err != nil {
		t.Fatal(err)
	}
	defer g.abort()
	evidence, gone := g.Finish(false)
	if !gone {
		t.Fatalf("empty guard did not verify closure: %s", evidence)
	}
	journal, err := r.ReadPostgreSQLLifecycle(req.ExecutionTag)
	if err != nil || !strings.Contains(string(journal), `"event":"finished"`) {
		t.Fatal("guard evidence was not durably journaled", err)
	}
	// An observer that already expired cannot acknowledge a later successful
	// CLI completion. Its valid metadata must remain available as uncertainty.
	req.ExecutionTag = "fwm_guard_expired_0123456789abcdef"
	req.statementBudgetSeconds = 1
	expired, err := r.startPostgreSQLGuard(ctx, req, root, work)
	if err != nil {
		t.Fatal(err)
	}
	defer expired.abort()
	time.Sleep(12 * time.Second) // The helper watchdog is its budget plus 10s.
	metadata, verified := expired.Finish(false)
	var stale PostgreSQLExecutionEvidence
	if json.Unmarshal(metadata, &stale) != nil || stale.Outcome != "cancelled_gone" || verified {
		t.Fatalf("expired observer was accepted as successful cleanup: %s (%t)", metadata, verified)
	}
}

func TestPostgreSQLGuardLiveNeverSignalsCopiedTag(t *testing.T) {
	if os.Getenv("FWM_POSTGRES_MIGRATION_LAB") != "1" {
		t.Skip("requires named disposable lab")
	}
	if os.Getenv("PGHOST") != "localhost" || os.Getenv("PGPORT") != "55440" {
		t.Fatal("requires named loopback disposable lab")
	}
	ca, err := os.ReadFile(os.Getenv("PGSSLROOTCERT"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(Config{Executable: os.Getenv("FWM_LAB_FLYWAY"), Version: "13.10.3", SHA256: "a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c", TimeoutSeconds: 12}, t.TempDir())
	root, err := r.distributionRoot()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err = os.WriteFile(filepath.Join(work, "ca.pem"), ca, 0600); err != nil {
		t.Fatal(err)
	}
	app := "fwm_copied_tag_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	req := Request{ExecutionTag: app, Host: "localhost", Port: 55440, DatabaseName: os.Getenv("PGDATABASE"), Username: os.Getenv("PGUSER"), Password: os.Getenv("PGPASSWORD"), CAPEM: string(ca), statementBudgetSeconds: 12}
	g, err := r.startPostgreSQLGuard(context.Background(), req, root, work)
	if err != nil {
		t.Fatal(err)
	}
	defer g.abort()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	// This deliberately copied tag belongs to a different client. The observer
	// must leave it alive even when it is asked to reconcile cancellation.
	client := exec.CommandContext(ctx, "docker", "exec", "-e", "PGPASSWORD", "-e", "PGAPPNAME="+app, "flyway-manager-postgres17-lab", "psql", "-XAtq", "-h", "localhost", "-U", req.Username, "-d", req.DatabaseName, "-c", "SELECT pg_sleep(15)")
	if err = client.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Wait() }()
	observation := func() string {
		b, e := exec.Command("docker", "exec", "flyway-manager-postgres17-lab", "psql", "-XAtq", "-U", "lab_admin", "-d", req.DatabaseName, "-c", "SELECT count(*) FROM pg_stat_activity WHERE application_name='"+app+"' AND state='active'").Output()
		if e != nil {
			t.Fatal(e)
		}
		return strings.TrimSpace(string(b))
	}
	deadline := time.Now().Add(5 * time.Second)
	for observation() != "1" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if observation() != "1" {
		t.Fatal("copied-tag client was not observed")
	}
	evidence, gone := g.Finish(true)
	if gone || observation() != "1" {
		t.Fatal("observer signaled or incorrectly cleared unrelated copied-tag session", string(evidence))
	}
	if err = <-done; err != nil {
		t.Fatal("unrelated copied-tag session did not finish normally", err)
	}
	journal, err := r.ReadPostgreSQLLifecycle(app)
	if err != nil || !strings.Contains(string(journal), `"state":"active"`) {
		t.Fatal("active snapshot was not retained", err)
	}
}
