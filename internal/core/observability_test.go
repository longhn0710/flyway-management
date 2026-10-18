package core

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flyway-manager/internal/observability"
)

func TestObservabilityRetainsFrozenRunScopeWithoutSQLOrCLIOutput(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	source, err := observability.OpenSource(context.Background(), f.dir, f.db.InstallationID)
	must(t, err)
	defer source.Close()
	canary := "SQL_CANARY_SHOULD_NEVER_REACH_OBSERVABILITY"
	in := uploadRun(t, f, target, schema, account, "V1__safe.sql", "SELECT '"+canary+"' FROM dual;")
	run, err := f.s.SubmitRun(testCtx, f.trace(), in, "observed-run")
	must(t, err)
	if waitRun(t, f, run.ID).Status != "SUCCEEDED" {
		t.Fatal("fixture migration failed")
	}
	counts := func() [4]int {
		var c [4]int
		must(t, f.db.SQL.QueryRow(`SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM migration_runs),(SELECT count(*) FROM run_executions),(SELECT count(*) FROM migration_streams)`).Scan(&c[0], &c[1], &c[2], &c[3]))
		return c
	}
	before := counts()
	events, _, err := source.Poll(testCtx)
	must(t, err)
	found := false
	for _, event := range events {
		encoded, err := json.Marshal(event)
		must(t, err)
		if strings.Contains(string(encoded), canary) || strings.Contains(string(encoded), account.Username) || strings.Contains(string(encoded), "jdbc:") {
			t.Fatal("private execution material exported")
		}
		if event.RunID == run.ID && event.Outcome == "SUCCEEDED" {
			found = true
			if event.InstanceID != target.ID || event.Schema != schema.Name || event.Engine != "oracle" || event.Database != target.Service || event.DurationMS == nil {
				t.Fatalf("missing frozen run metadata: %+v", event)
			}
		}
	}
	if !found {
		t.Fatal("committed successful run was not projected")
	}
	if counts() != before {
		t.Fatal("observer created/replayed execution")
	}
}

func TestObservabilityOutageDoesNotBlockMigrationOrLocalAudit(t *testing.T) {
	f, _, target, schema, account := executionFixture(t)
	var attempts atomic.Int64
	collector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer collector.Close()
	dir := t.TempDir()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: collector.Certificate().Raw})
	key, err := x509.MarshalPKCS8PrivateKey(collector.TLS.Certificates[0].PrivateKey)
	must(t, err)
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	certPath, keyPath := filepath.Join(dir, "identity.pem"), filepath.Join(dir, "identity.key")
	must(t, os.WriteFile(certPath, certificate, 0600))
	must(t, os.WriteFile(keyPath, private, 0600))
	pipeline, err := observability.Start(testCtx, observability.Config{Enabled: true, CollectorEndpoint: collector.URL, CAFile: certPath, ClientCertificateFile: certPath, ClientKeyFile: keyPath}, f.dir, f.db.InstallationID, nil)
	must(t, err)
	defer pipeline.Close()
	_, err = f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: "Establish observed export outage"})
	must(t, err)
	deadline := time.Now().Add(5 * time.Second)
	for attempts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if attempts.Load() == 0 {
		t.Fatal("test did not observe an actual export outage")
	}
	input := uploadRun(t, f, target, schema, account, "V1__outage.sql", "SELECT 1 FROM dual;")
	run, err := f.s.SubmitRun(testCtx, f.trace(), input, "during-export-outage")
	must(t, err)
	if got := waitRun(t, f, run.ID); got.Status != "SUCCEEDED" {
		t.Fatalf("outage blocked migration: %s", got.Status)
	}
	var audits int
	must(t, f.db.SQL.QueryRow(`SELECT count(*) FROM audit_events WHERE action='migration.state' AND target=? AND outcome='SUCCEEDED'`, run.ID).Scan(&audits))
	if audits != 1 {
		t.Fatal("local execution audit did not commit")
	}
}
