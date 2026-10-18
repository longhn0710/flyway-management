package observability

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flyway-manager/internal/core"
	"flyway-manager/internal/database"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type eventFixture struct {
	db      *database.DB
	service *core.Service
	dir     string
	trace   core.Trace
}

type eventCounter struct{ n atomic.Int64 }

func (e *eventCounter) Export(_ context.Context, records []sdklog.Record) error {
	e.n.Add(int64(len(records)))
	return nil
}
func (e *eventCounter) Shutdown(context.Context) error   { return nil }
func (e *eventCounter) ForceFlush(context.Context) error { return nil }

func TestPipelineDailyIntakeBudgetAndRollover(t *testing.T) {
	exporter := new(eventCounter)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	defer provider.Shutdown(context.Background())
	p := &Pipeline{provider: provider, logger: provider.Logger("budget-test")}
	at := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	e, ok := project(strings.Repeat("a", 32), persistedEvent{id: 1, timestamp: at.Unix(), action: "auth.login", outcome: "failure"})
	if !ok {
		t.Fatal("fixture event rejected")
	}
	for i := 0; i < 5010; i++ {
		p.emit(e, at)
	}
	if exporter.n.Load() != 5000 || p.stats.BudgetDropped.Load() != 10 {
		t.Fatal("daily intake did not stop at budget")
	}
	p.emit(e, at.Add(24*time.Hour))
	if exporter.n.Load() != 5001 {
		t.Fatal("new UTC day did not resume collection")
	}
}

func mutualTLSFixture(t *testing.T, handler http.Handler) (*httptest.Server, Config) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated-fixture-ca"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	makeIdentity := func(serial int64, client bool) (tls.Certificate, []byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "flyway-manager"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		if client {
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			template.IPAddresses = nil
		}
		der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		private, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		certPEM, keyPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return pair, certPEM, keyPEM
	}
	serverIdentity, _, _ := makeIdentity(2, false)
	_, clientPEM, clientKey := makeIdentity(3, true)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverIdentity}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}
	server.StartTLS()
	dir := t.TempDir()
	cfg := Config{Enabled: true, CollectorEndpoint: server.URL, CAFile: filepath.Join(dir, "ca.pem"), ClientCertificateFile: filepath.Join(dir, "client.pem"), ClientKeyFile: filepath.Join(dir, "client.key")}
	for path, data := range map[string][]byte{cfg.CAFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), cfg.ClientCertificateFile: clientPEM, cfg.ClientKeyFile: clientKey} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return server, cfg
}

func newEventFixture(t *testing.T) eventFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := database.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := core.New(ctx, db, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(s.SetupTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := s.Bootstrap(ctx, core.Trace{RequestID: "bootstrap"}, core.UserInput{Username: "admin", Password: "local-fixture-secret-canary"}, strings.TrimSpace(string(token)))
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AuthenticateSession(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	return eventFixture{db, s, dir, core.Trace{RequestID: "controlled_correlation", Actor: a}}
}
func (f eventFixture) project(t *testing.T, name string) core.Project {
	t.Helper()
	p, err := f.service.SaveProject(context.Background(), f.trace, "", core.ProjectInput{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSourceOnlyNewCommittedEventsAndExplicitRedaction(t *testing.T) {
	f := newEventFixture(t)
	ctx := context.Background()
	s, err := OpenSource(ctx, f.dir, f.db.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if events, _, err := s.Poll(ctx); err != nil || len(events) != 0 {
		t.Fatal("historical audits exported", err)
	}
	canary := "PASSWORD_TOKEN_SQL_JDBC_CANARY"
	p := f.project(t, canary)
	trace := f.trace
	trace.Actor.Name = canary
	trace.RequestID = "jdbc:secret.invalid?password=" + canary
	if err = f.service.Event(ctx, trace, "auth.login", "jdbc:database?password="+canary, "failure"); err != nil {
		t.Fatal(err)
	}
	if err = f.service.Event(ctx, trace, canary, canary, "success"); err != nil {
		t.Fatal(err)
	}
	if err = f.service.Event(ctx, trace, "auth.oidc", "oidc", canary); err != nil {
		t.Fatal(err)
	}
	// The uncommitted writer must not block the read-only WAL reader or leak an event.
	tx, err := f.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) VALUES('','','private','auth.oidc','oidc',?,'failure','later')`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	events, rejected, err := s.Poll(readCtx)
	cancel()
	if err != nil || len(events) != 2 || rejected != 2 {
		t.Fatalf("committed projection count=%d rejected=%d error=%v", len(events), rejected, err)
	}
	if events[0].ProjectID != p.ID || events[0].ActorID != f.trace.Actor.ID || events[0].AuthenticationProvider != "local" {
		t.Fatal("safe identity context missing")
	}
	if events[1].CorrelationID != "" {
		t.Fatal("unsafe correlation exported")
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), canary) || strings.Contains(string(encoded), "jdbc:") || strings.Contains(string(encoded), "private") {
		t.Fatal("classified source data exported")
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	later, _, err := s.Poll(ctx)
	if err != nil || len(later) != 1 || later[0].CorrelationID != "later" {
		t.Fatal("committed event not observed", err)
	}
	if _, err = s.db.Exec(`UPDATE projects SET name='must not write'`); err == nil {
		t.Fatal("telemetry connection can write")
	}
	if f.db.SchemaVersion != 8 {
		t.Fatal("export changed schema version")
	}
	if len(encoded) > 4096*len(events) {
		t.Fatal("event bound exceeded")
	}
	if again, _, err := s.Poll(ctx); err != nil || len(again) != 0 {
		t.Fatal("events replayed", err)
	}
	// A reader restart deliberately skips backlog, rather than silently backfilling.
	restarted, err := OpenSource(ctx, f.dir, f.db.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if events, _, err = restarted.Poll(ctx); err != nil || len(events) != 0 {
		t.Fatal("restart exported historical data", err)
	}
}

func TestSourceBatchLimitAndStableIDs(t *testing.T) {
	f := newEventFixture(t)
	ctx := context.Background()
	s, err := OpenSource(ctx, f.dir, f.db.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := f.db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < BatchSize+3; i++ {
		if _, err = tx.Exec(`INSERT INTO audit_events(actor_id,actor_type,actor_name,action,target,timestamp,outcome,request_id) VALUES('','','','auth.oidc','oidc',?,'failure','batch')`, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	first, _, err := s.Poll(ctx)
	if err != nil || len(first) != BatchSize {
		t.Fatal("unbounded batch", err)
	}
	second, _, err := s.Poll(ctx)
	if err != nil || len(second) != 3 {
		t.Fatal("tail lost", err)
	}
	ids := map[string]bool{}
	for _, event := range append(first, second...) {
		if ids[event.EventID] {
			t.Fatal("duplicate identity")
		}
		ids[event.EventID] = true
	}
	in := persistedEvent{id: 77, timestamp: time.Now().Unix(), action: "auth.login", outcome: "failure"}
	a, _ := project(f.db.InstallationID, in)
	b, _ := project(f.db.InstallationID, in)
	if a.EventID != b.EventID {
		t.Fatal("retry changed event identity")
	}
}

func TestPipelineVerifiedTLSWireTypesRedactionAndNonBlockingOutage(t *testing.T) {
	f := newEventFixture(t)
	ctx := context.Background()
	var status atomic.Int64
	var unavailable atomic.Int64
	status.Store(200)
	received := make(chan *collectorpb.ExportLogsServiceRequest, 20)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer ambient-secret-canary")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "X-Private-Claim=ambient-secret-canary")
	server, cfg := mutualTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 || r.TLS.PeerCertificates[0].Subject.CommonName != "flyway-manager" {
			t.Error("Manager client identity not verified")
		}
		if r.URL.Path != "/v1/logs" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Private-Claim") != "" {
			t.Error("wrong path or ambient credentials forwarded")
		}
		payload, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if err != nil {
			t.Error(err)
			return
		}
		request := new(collectorpb.ExportLogsServiceRequest)
		if err = proto.Unmarshal(payload, request); err != nil {
			t.Error(err)
			return
		}
		if status.Load() != 200 {
			unavailable.Add(1)
			w.WriteHeader(int(status.Load()))
			return
		}
		received <- request
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}))
	defer server.Close()
	p, err := Start(ctx, cfg, f.dir, f.db.InstallationID, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	canary := "DO_NOT_EXPORT_SECRET_SQL_CIPHERTEXT"
	created := f.project(t, canary)
	var wire *collectorpb.ExportLogsServiceRequest
	select {
	case wire = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("event did not reach TLS collector fixture")
	}
	encoded, _ := protojson.Marshal(wire)
	if strings.Contains(string(encoded), canary) || strings.Contains(string(encoded), "ambient-secret-canary") {
		t.Fatal("secret on OTLP wire")
	}
	resources := wire.ResourceLogs[0].Resource.Attributes
	attrs := map[string]string{}
	for _, kv := range resources {
		attrs[kv.Key] = kv.Value.GetStringValue()
	}
	if attrs["service.name"] != "flyway-manager" || attrs["deployment.environment"] != "homelab" || attrs["service.instance.id"] != f.db.InstallationID {
		t.Fatal("resource identity incorrect")
	}
	record := wire.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	var body Event
	if err = json.Unmarshal([]byte(record.Body.GetStringValue()), &body); err != nil {
		t.Fatal(err)
	}
	if body.ProjectID != created.ID || body.SchemaVersion != 1 || body.CorrelationID != f.trace.RequestID {
		t.Fatal("wire context incorrect")
	}
	for _, kv := range record.Attributes {
		if kv.Key == "schema_version" && kv.Value.GetIntValue() != 1 {
			t.Fatal("schema version lost numeric type")
		}
	}
	// A metered destination outage never enters the local application transaction.
	status.Store(503)
	outage := f.project(t, "During outage")
	deadline := time.Now().Add(5 * time.Second)
	for unavailable.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if unavailable.Load() == 0 {
		t.Fatal("outage was not exercised")
	}
	start := time.Now()
	during := f.project(t, "During observed outage")
	if time.Since(start) > time.Second {
		t.Fatal("local audit blocked by collector outage")
	}
	status.Store(200)
	after := f.project(t, "After restoration")
	seen := map[string]bool{created.ID: true}
	deadline = time.Now().Add(8 * time.Second)
	for len(seen) < 4 && time.Now().Before(deadline) {
		select {
		case request := <-received:
			for _, resource := range request.ResourceLogs {
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						var e Event
						if err := json.Unmarshal([]byte(record.Body.GetStringValue()), &e); err != nil {
							t.Fatal(err)
						}
						seen[e.ProjectID] = true
					}
				}
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !seen[outage.ID] || !seen[during.ID] || !seen[after.ID] {
		t.Fatal("collection did not recover retained records and new events")
	}
	var audits int
	if err = f.db.SQL.QueryRow(`SELECT count(*) FROM audit_events WHERE action='project.create'`).Scan(&audits); err != nil || audits != 4 {
		t.Fatal("local authoritative audit missing", err)
	}
}

func TestPipelineDisabledInvalidAndUntrustedTLS(t *testing.T) {
	if pipeline, err := Start(context.Background(), Config{}, "missing", "bad", nil); pipeline != nil || err != nil {
		t.Fatal("disabled export accessed registry", err)
	}
	f := newEventFixture(t)
	for _, endpoint := range []string{"http://collector:4318", "https://user:password@collector", "https://collector/path", "https://collector?token=secret"} {
		if p, err := Start(context.Background(), Config{Enabled: true, CollectorEndpoint: endpoint}, f.dir, f.db.InstallationID, nil); err == nil || p != nil {
			t.Fatal("unsafe collector URL accepted")
		}
	}
	var calls atomic.Int64
	server, cfg := mutualTLSFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	// Confirm this is not accidentally trusted by the host certificate pool.
	if _, err := server.Certificate().Verify(x509.VerifyOptions{}); err == nil {
		t.Fatal("fixture certificate unexpectedly trusted")
	}
	cfg.CAFile = ""
	p, err := Start(context.Background(), cfg, f.dir, f.db.InstallationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	f.project(t, "TLS rejection preserves local action")
	deadline := time.Now().Add(12 * time.Second)
	for p.stats.ExportFailed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() != 0 || p.stats.ExportFailed.Load() == 0 {
		t.Fatal("untrusted TLS accepted or rejection missing")
	}
	start := time.Now()
	p.Close()
	if time.Since(start) > 2*time.Second {
		t.Fatal("export shutdown was unbounded")
	}
}
