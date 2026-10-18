package observability

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	collectorpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

// Opt-in: executes only the pinned Collector against private local TLS fixtures.
// No cluster credentials, Elastic key or external destination is read.
func TestCollectorLiveMutualTLSAndIndependentAllowlist(t *testing.T) {
	image := os.Getenv("FWM_TEST_COLLECTOR_IMAGE")
	if image == "" {
		t.Skip("set FWM_TEST_COLLECTOR_IMAGE to the pinned proposal digest")
	}
	const pinned = "otel/opentelemetry-collector-contrib@sha256:1175b03003053962012f5a231f5567f66a3e8fdb960c7835c0e48f7aa1555806"
	if image != pinned {
		t.Fatal("acceptance requires proposal image digest")
	}
	const fixtureKey = "isolated-collector-fixture-012345678901234567890123456789"
	received := make(chan *collectorpb.ExportLogsServiceRequest, 10)
	template, cfg := mutualTLSFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer template.Close()
	downstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logs" || r.Header.Get("Authorization") != "ApiKey "+fixtureKey {
			t.Error("collector destination/authentication contract mismatch")
			w.WriteHeader(400)
			return
		}
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zipped, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error("invalid gzip")
				w.WriteHeader(400)
				return
			}
			defer zipped.Close()
			reader = zipped
		}
		payload, _ := io.ReadAll(io.LimitReader(reader, 256<<10))
		request := new(collectorpb.ExportLogsServiceRequest)
		if proto.Unmarshal(payload, request) != nil {
			t.Error("invalid exported OTLP")
			w.WriteHeader(400)
			return
		}
		received <- request
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	downstream.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: template.TLS.Certificates}
	downstream.StartTLS()
	defer downstream.Close()
	dir := filepath.Dir(cfg.CAFile)
	authDir := filepath.Join(dir, "auth")
	if err := os.Mkdir(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	authScript, err := filepath.Abs("../../deploy/observability/prepare-auth.py")
	if err != nil {
		t.Fatal(err)
	}
	const authImage = "python@sha256:a1165e272e578941b84abc79e4ab38a0305cd12803a5c4247979ac7655f4d641"
	init := exec.Command("docker", "run", "--rm", "--pull=never", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory=32m", "--cpus=0.1", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--mount", "type=bind,src="+authScript+",dst=/prepare-auth.py,readonly", "--mount", "type=bind,src="+authDir+",dst=/run/collector-auth", "--env", "OTEL_EXPORTER_OTLP_HEADERS=Authorization=ApiKey%20"+fixtureKey, authImage, "python3", "-B", "/prepare-auth.py")
	var authOutput bytes.Buffer
	init.Stdout = &authOutput
	init.Stderr = io.Discard
	if err := init.Run(); err != nil || authOutput.String() != "OTLP_AUTH_HEADER_FORMAT_VALID\n" {
		t.Fatal("pinned auth initializer failed")
	}
	info, err := os.Stat(filepath.Join(authDir, "headers.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("auth file is not private")
	}
	identity := template.TLS.Certificates[0]
	private, err := x509.MarshalPKCS8PrivateKey(identity.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"server.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: identity.Certificate[0]}), "server.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})} {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	metricsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	metricsPort := metricsListener.Addr().(*net.TCPAddr).Port
	metricsListener.Close()
	raw, err := os.ReadFile("../../deploy/observability/collector.yaml")
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)
	replacements := map[string]string{
		"port: 8888":                 "port: " + strconv.Itoa(metricsPort),
		"0.0.0.0:4318":               "127.0.0.1:" + strconv.Itoa(port),
		"/run/collector-tls/tls.crt": "/fixture/server.pem",
		"/run/collector-tls/tls.key": "/fixture/server.key",
		"/run/collector-tls/ca.crt":  "/fixture/ca.pem",
		"https://my-observability-project-d9be29.ingest.asia-southeast1.gcp.elastic.cloud:443": downstream.URL,
		"/run/collector-auth/headers.json": "/fixture/auth/headers.json",
		"insecure: false":                  "ca_file: /fixture/ca.pem\n      insecure: false",
	}
	for from, to := range replacements {
		config = strings.ReplaceAll(config, from, to)
	}
	if err = os.WriteFile(filepath.Join(dir, "collector.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := fmt.Sprintf("fwm-collector-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "--pull=never", "--name", name, "--network", "host", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--mount", "type=bind,src="+dir+",dst=/fixture,readonly", image, "--config=/fixture/collector.yaml")
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		t.Fatal("collector could not start")
	}
	t.Cleanup(func() { _ = exec.Command("docker", "stop", "--time", "1", name).Run(); cancel(); _ = command.Wait() })
	cfg.CollectorEndpoint = "https://127.0.0.1:" + strconv.Itoa(port)
	caPEM, _ := os.ReadFile(cfg.CAFile)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	pair, err := tls.LoadX509KeyPair(cfg.ClientCertificateFile, cfg.ClientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, Certificates: []tls.Certificate{pair}}}, Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, e := client.Get(cfg.CollectorEndpoint)
		if e == nil {
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("collector receiver did not become available")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// A trusted CA without the dedicated client identity cannot submit.
	anonymous := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}, Timeout: time.Second}
	response, e := anonymous.Post(cfg.CollectorEndpoint+"/v1/logs", "application/json", strings.NewReader(`{}`))
	if e == nil {
		response.Body.Close()
		t.Fatal("collector accepted missing client identity")
	}
	f := newEventFixture(t)
	pipeline, err := Start(context.Background(), cfg, f.dir, f.db.InstallationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pipeline.Close()
	project := f.project(t, "PRIVATE_PROJECT_NAME")
	// Deliberately bypass the producer to prove independent collector stripping.
	payload := `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"flyway-manager"}},{"key":"deployment.environment","value":{"stringValue":"homelab"}},{"key":"private.resource","value":{"stringValue":"PRIVATE_CANARY"}}]},"scopeLogs":[{"logRecords":[{"body":{"stringValue":"PRIVATE_CANARY"},"attributes":[{"key":"schema_version","value":{"intValue":"1"}},{"key":"event_id","value":{"stringValue":"collector-allowlist-proof"}},{"key":"private.attribute","value":{"stringValue":"PRIVATE_CANARY"}}]}]}]}]}`
	response, err = client.Post(cfg.CollectorEndpoint+"/v1/logs", "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal("collector input failed")
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("collector rejected valid fixture")
	}
	foreign := strings.ReplaceAll(strings.ReplaceAll(payload, "flyway-manager", "unapproved-service"), "collector-allowlist-proof", "FOREIGN_EVENT")
	response, err = client.Post(cfg.CollectorEndpoint+"/v1/logs", "application/json", strings.NewReader(foreign))
	if err != nil {
		t.Fatal("foreign-source rejection fixture failed")
	}
	response.Body.Close()
	foundProject, foundProof := false, false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case request := <-received:
			encoded, _ := proto.Marshal(request)
			if bytes.Contains(encoded, []byte("PRIVATE_")) || bytes.Contains(encoded, []byte("FOREIGN_EVENT")) {
				t.Fatal("independent collector allowlist leaked fixture")
			}
			for _, resource := range request.ResourceLogs {
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						if record.Body.GetStringValue() != "flyway-manager.event.v1" {
							t.Fatal("collector retained arbitrary body")
						}
						for _, attribute := range record.Attributes {
							if attribute.Key == "project_id" && attribute.Value.GetStringValue() == project.ID {
								foundProject = true
							}
							if attribute.Key == "event_id" && attribute.Value.GetStringValue() == "collector-allowlist-proof" {
								foundProof = true
							}
						}
					}
				}
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	metricsResponse, err := http.Get("http://127.0.0.1:" + strconv.Itoa(metricsPort) + "/metrics")
	if err != nil {
		t.Fatal("internal collector metrics unavailable")
	}
	metrics, _ := io.ReadAll(io.LimitReader(metricsResponse.Body, 1<<20))
	metricsResponse.Body.Close()
	if !bytes.Contains(metrics, []byte("otelcol_receiver_accepted_log_records")) || !bytes.Contains(metrics, []byte("otelcol_exporter_sent_log_records")) {
		t.Fatal("collector transport counters missing")
	}
	if !foundProject || !foundProof {
		t.Fatal("collector did not forward controlled event and independent allowlist proof")
	}
}
