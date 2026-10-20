package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flyway-manager/internal/core"
)

func TestCancelledDenialAuditPreservesV1AndV2Classification(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		for _, auditFails := range []bool{false, true} {
			t.Run(version+map[bool]string{false: "-durable", true: "-audit-fails"}[auditFails], func(t *testing.T) {
				f := apiSetup(t)
				if auditFails {
					if _, err := f.db.SQL.Exec("CREATE TRIGGER reject_all_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'private-storage-detail'); END;"); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				ctx = context.WithValue(ctx, actorKey{}, core.Actor{ID: f.admin.ID, Kind: "user", Name: f.admin.Username, Role: "Admin"})
				cancel()
				req := httptest.NewRequest("POST", "http://manager.test/api/"+version+"/denied", nil).WithContext(ctx)
				rec := httptest.NewRecorder()
				h := phase2{s: f.service, logger: slog.New(slog.NewJSONHandler(f.logs, nil))}
				h.fail(rec, req, &core.Error{Status: 403, Code: "FORBIDDEN", Message: "Access denied."})
				wantStatus, wantCode := 403, "FORBIDDEN"
				if auditFails {
					wantStatus, wantCode = 500, "AUDIT_FAILURE"
				}
				assertStatus(t, rec, wantStatus)
				var out map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if version == "v1" {
					out = out["error"].(map[string]any)
				}
				if out["code"] != wantCode {
					t.Fatal(out)
				}
				var n int
				if err := f.db.SQL.QueryRow("SELECT count(*) FROM audit_events WHERE action='request.denied'").Scan(&n); err != nil {
					t.Fatal(err)
				}
				if (!auditFails && n != 1) || (auditFails && n != 0) {
					t.Fatal("incorrect security audit persistence", n)
				}
			})
		}
	}
}

func TestCorrectionOperationBudgetsRemainBounded(t *testing.T) {
	f := apiSetup(t)
	h := phase2{s: f.service}
	for path, want := range map[string]time.Duration{
		"/api/v2/projects/p/deployment-targets/d/correction-reviews":   50 * time.Second,
		"/api/v2/projects/p/deployment-targets/d/correction-approvals": 50 * time.Second,
		"/api/v2/projects/p/deployment-targets/d/preflights":           50 * time.Second,
		"/api/v2/projects": 20 * time.Second,
	} {
		var remaining time.Duration
		next := h.ready(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			d, ok := ctx.Deadline()
			if !ok {
				t.Fatal("unbounded request")
			}
			remaining = time.Until(d)
		}))
		next.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", path, nil))
		if remaining > want || remaining < want-time.Second {
			t.Fatalf("%s budget=%s, want %s", path, remaining, want)
		}
	}
}
