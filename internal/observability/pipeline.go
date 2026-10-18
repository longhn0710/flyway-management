package observability

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

// Config contains no Elastic credential: only the separately approved internal
// Collector destination, trust and client identity paths. Nothing starts when disabled.
type Config struct {
	Enabled               bool   `json:"enabled"`
	CollectorEndpoint     string `json:"collector_endpoint"`
	CAFile                string `json:"ca_file"`
	ClientCertificateFile string `json:"client_certificate_file"`
	ClientKeyFile         string `json:"client_key_file"`
}

type Stats struct {
	Projected, Rejected, ReadErrors, BudgetDropped, Accepted, ExportFailed atomic.Uint64
}

type Pipeline struct {
	source              *Source
	provider            *sdklog.LoggerProvider
	logger              otellog.Logger
	stats               Stats
	cancel              context.CancelFunc
	done                chan struct{}
	once                sync.Once
	day                 string
	dayEvents, dayBytes int
}

// Start creates no network connection synchronously. Errors are optional-export
// configuration/read errors and must never prevent the Manager from starting.
func Start(ctx context.Context, cfg Config, dataDir, installation string, diagnostics *slog.Logger) (*Pipeline, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	u, err := url.Parse(cfg.CollectorEndpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("observability requires an HTTPS collector origin")
	}
	if cfg.ClientCertificateFile == "" || cfg.ClientKeyFile == "" {
		return nil, errors.New("observability requires a client TLS identity")
	}
	identity, err := tls.LoadX509KeyPair(cfg.ClientCertificateFile, cfg.ClientKeyFile)
	if err != nil {
		return nil, errors.New("observability client TLS identity unavailable")
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("observability trust unavailable")
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil || len(pem) > 64<<10 || !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("observability CA unavailable")
		}
	}
	readCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	source, err := OpenSource(readCtx, dataDir, installation)
	cancel()
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, Certificates: []tls.Certificate{identity}}, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("observability redirects rejected") }}
	// Explicit endpoint, path, headers and transport override ambient OTEL/proxy
	// configuration. The application never receives or forwards the Elastic key.
	exporter, err := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(u.Scheme+"://"+u.Host), otlploghttp.WithURLPath("/v1/logs"), otlploghttp.WithHeaders(map[string]string{}), otlploghttp.WithHTTPClient(client), otlploghttp.WithTimeout(10*time.Second), otlploghttp.WithMaxRequestSize(256<<10), otlploghttp.WithMaxResponseSize(64<<10), otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: true, InitialInterval: time.Second, MaxInterval: 5 * time.Second, MaxElapsedTime: 60 * time.Second}))
	if err != nil {
		source.Close()
		return nil, errors.New("observability exporter unavailable")
	}
	loopCtx, stop := context.WithCancel(ctx)
	p := &Pipeline{source: source, cancel: stop, done: make(chan struct{})}
	counted := &countingExporter{inner: exporter, stats: &p.stats}
	processor := sdklog.NewBatchProcessor(counted, sdklog.WithMaxQueueSize(1024), sdklog.WithExportMaxBatchSize(64), sdklog.WithExportInterval(time.Second), sdklog.WithExportTimeout(10*time.Second))
	p.provider = sdklog.NewLoggerProvider(sdklog.WithProcessor(processor), sdklog.WithAttributeCountLimit(32), sdklog.WithAttributeValueLengthLimit(128), sdklog.WithResource(resource.NewSchemaless(attribute.String("service.name", "flyway-manager"), attribute.String("deployment.environment", "homelab"), attribute.String("service.instance.id", installation), attribute.String("data_stream.dataset", "flyway_manager.events"), attribute.String("data_stream.namespace", "homelab"))))
	p.logger = p.provider.Logger("flyway-manager.events.v1")
	go p.run(loopCtx, diagnostics)
	return p, nil
}

func (p *Pipeline) run(ctx context.Context, diagnostics *slog.Logger) {
	defer close(p.done)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	status := time.NewTicker(time.Minute)
	defer status.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			readCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			events, rejected, err := p.source.Poll(readCtx)
			cancel()
			p.stats.Rejected.Add(uint64(rejected))
			if err != nil {
				p.stats.ReadErrors.Add(1)
				continue
			}
			for _, e := range events {
				p.emit(e, time.Now())
			}
		case <-status.C:
			if diagnostics != nil {
				diagnostics.Info("observability counters", "projected", p.stats.Projected.Load(), "rejected", p.stats.Rejected.Load(), "read_errors", p.stats.ReadErrors.Load(), "budget_dropped", p.stats.BudgetDropped.Load(), "transport_accepted", p.stats.Accepted.Load(), "transport_failed", p.stats.ExportFailed.Load())
			}
		}
	}
}

func (p *Pipeline) emit(e Event, at time.Time) {
	body, _ := json.Marshal(e)
	day := at.UTC().Format("2006-01-02")
	if p.day != day {
		p.day = day
		p.dayEvents = 0
		p.dayBytes = 0
	}
	// Conservative local intake bounds, not a billing guarantee. Counts reset on
	// process restart; indexed/enriched volume and duplicate retries may be larger.
	if p.dayEvents >= 5000 || p.dayBytes+len(body) > 10<<20 {
		p.stats.BudgetDropped.Add(1)
		return
	}
	p.dayEvents++
	p.dayBytes += len(body)
	var r otellog.Record
	ts, _ := time.Parse(time.RFC3339, e.Timestamp)
	r.SetTimestamp(ts)
	r.SetObservedTimestamp(at)
	r.SetEventName("flyway-manager.event.v1")
	r.SetBody(attribute.StringValue(string(body)))
	level := otellog.SeverityInfo
	if e.Severity == "WARN" {
		level = otellog.SeverityWarn
	}
	if e.Severity == "ERROR" {
		level = otellog.SeverityError
	}
	r.SetSeverity(level)
	r.SetSeverityText(e.Severity)
	r.AddAttributes(attribute.Int("schema_version", e.SchemaVersion), attribute.String("event_id", e.EventID), attribute.String("timestamp", e.Timestamp), attribute.String("event_type", e.EventType), attribute.String("severity", e.Severity), attribute.String("authentication_provider", e.AuthenticationProvider), attribute.String("action", e.Action), attribute.String("outcome", e.Outcome))
	for key, value := range map[string]string{"project_id": e.ProjectID, "instance_id": e.InstanceID, "engine": e.Engine, "database": e.Database, "schema": e.Schema, "run_id": e.RunID, "correlation_id": e.CorrelationID, "actor_id": e.ActorID} {
		if value != "" {
			r.AddAttributes(attribute.String(key, value))
		}
	}
	if e.DurationMS != nil {
		r.AddAttributes(attribute.Int64("duration_ms", *e.DurationMS))
	}
	p.logger.Emit(context.Background(), r)
	p.stats.Projected.Add(1)
}

func (p *Pipeline) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.cancel()
		<-p.done
		flush, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.provider.Shutdown(flush)
		_ = p.source.Close()
	})
}

type countingExporter struct {
	inner sdklog.Exporter
	stats *Stats
}

func (e *countingExporter) Export(ctx context.Context, records []sdklog.Record) error {
	if err := e.inner.Export(ctx, records); err != nil {
		e.stats.ExportFailed.Add(uint64(len(records)))
		return errors.New("Manager observability transport unavailable")
	}
	e.stats.Accepted.Add(uint64(len(records)))
	return nil
}
func (e *countingExporter) Shutdown(ctx context.Context) error   { return e.inner.Shutdown(ctx) }
func (e *countingExporter) ForceFlush(ctx context.Context) error { return e.inner.ForceFlush(ctx) }
