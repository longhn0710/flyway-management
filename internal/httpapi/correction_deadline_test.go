package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flyway-manager/internal/core"
	"flyway-manager/internal/flyway"
)

func correctionRequest(f *correctionHTTPFixture, ctx context.Context, suffix string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", fmt.Sprintf("http://manager.test/api/v2/projects/%s/deployment-targets/%s/%s", f.d.ProjectID, f.d.ID, suffix), bytes.NewReader(raw)).WithContext(ctx)
	req.Header.Set("Origin", "http://manager.test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", f.f.csrf)
	req.AddCookie(f.f.cookie)
	rec := httptest.NewRecorder()
	f.f.handler.ServeHTTP(rec, req)
	return rec
}
func reviewBody(f *correctionHTTPFixture, failed, artifact string) core.CorrectionReviewInput {
	return core.CorrectionReviewInput{FailedRunID: failed, ArtifactID: artifact, DeploymentTargetRevision: f.d.Revision}
}
func assertCorrectionHeld(t *testing.T, f *correctionHTTPFixture, aid string, reviews int) {
	t.Helper()
	var n int
	for query, want := range map[string]int{
		"SELECT count(*) FROM migration_preflights WHERE artifact_id='" + aid + "'": reviews,
		"SELECT count(*) FROM oracle_correction_approvals":                          0,
		"SELECT held FROM migration_streams WHERE schema_id='" + f.d.SchemaID + "'": 1,
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := f.f.db.SQL.QueryRowContext(ctx, query).Scan(&n)
		cancel()
		if err != nil || n != want {
			t.Fatalf("%s = %d, want %d (%v)", query, n, want, err)
		}
	}
	f.runner.mu.Lock()
	defer f.runner.mu.Unlock()
	if f.runner.attempts["V6__step.sql"] != 1 || f.runner.repairCount != 1 {
		t.Fatal("review/approval repeated SQL or Repair")
	}
}
func assertProblemCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	assertStatus(t, rec, status)
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != code {
		t.Fatalf("problem=%s, want %s (%v)", rec.Body, code, err)
	}
}

// Exceed the old real 20-second HTTP budget. While the read-only probe is slow,
// the single SQLite connection must remain available to other requests.
func TestCorrectionReviewSlowEvidenceExceedsOldDeadline(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, _ := f.upload(t, fixedCorrectionFiles(), "slow-review")
	started := make(chan struct{})
	var once sync.Once
	f.runner.mu.Lock()
	f.runner.evidenceHook = func(ctx context.Context) error {
		var err error
		once.Do(func() {
			close(started)
			timer := time.NewTimer(21 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				err = ctx.Err()
			}
		})
		return err
	}
	f.runner.mu.Unlock()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- correctionRequest(f, context.Background(), "correction-reviews", reviewBody(f, rid, aid))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("review did not reach probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	var n int
	err := f.f.db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM oracle_correction_approvals").Scan(&n)
	cancel()
	if err != nil || n != 0 {
		t.Fatal("Oracle probe held SQLite transaction/connection", err)
	}
	select {
	case rec := <-done:
		review := decodeData[map[string]any](t, rec, 200)
		if review["ready"] != true || review["live_status"] != "VERIFIED" {
			t.Fatal(review)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("slow review did not finish within bounded operation")
	}
	assertCorrectionHeld(t, f, aid, 1)
}

func TestCorrectionDeadlinesNeverPersistReviewOrApproval(t *testing.T) {
	for _, stage := range []string{"initial-evidence", "info", "validate", "final-evidence", "approval"} {
		t.Run(stage, func(t *testing.T) {
			f := newCorrectionHTTPFixture(t)
			rid := prepareCorrectionFailure(t, f)
			aid, sha := f.upload(t, fixedCorrectionFiles(), "deadline-review")
			suffix := "correction-reviews"
			var body any = reviewBody(f, rid, aid)
			reviews := 0
			if stage == "approval" {
				review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
				reviews = 1
				suffix = "correction-approvals"
				body = core.CorrectionApprovalInput{PreflightID: review["id"].(string), InputSHA256: sha, PartialDDLReviewed: true, Note: "Independent fixture review"}
			}
			var evidenceCalls atomic.Int32
			wait := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
			f.runner.mu.Lock()
			f.runner.evidenceHook = func(ctx context.Context) error {
				n := evidenceCalls.Add(1)
				if err := ctx.Err(); err != nil {
					return err
				}
				if stage == "initial-evidence" || stage == "approval" || (stage == "final-evidence" && n == 2) {
					return wait(ctx)
				}
				return nil
			}
			f.runner.executeHook = func(ctx context.Context, req flyway.Request) error {
				if req.Command == stage {
					return wait(ctx)
				}
				return nil
			}
			f.runner.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			rec := correctionRequest(f, ctx, suffix, body)
			assertProblemCode(t, rec, 504, "REQUEST_TIMEOUT")
			assertCorrectionHeld(t, f, aid, reviews)
			if (stage == "info" || stage == "validate") && evidenceCalls.Load() != 1 {
				t.Fatal("review probed again after canceled preflight", evidenceCalls.Load())
			}
			var denied int
			if err := f.f.db.SQL.QueryRow("SELECT count(*) FROM audit_events WHERE action='request.denied' AND request_id=?", rec.Header().Get("X-Request-ID")).Scan(&denied); err != nil || denied == 0 {
				t.Fatal("timeout denial audit was suppressed", err)
			}
			if stage == "info" || stage == "validate" {
				var notVerified int
				if err := f.f.db.SQL.QueryRow("SELECT count(*) FROM audit_events WHERE action='migration.preflight' AND outcome='NOT_VERIFIED' AND request_id=?", rec.Header().Get("X-Request-ID")).Scan(&notVerified); err != nil || notVerified == 0 {
					t.Fatal("missing durable NOT_VERIFIED", err)
				}
			}
		})
	}
}

func TestCorrectionAuditFailureRollsBackReviewAndApproval(t *testing.T) {
	for _, action := range []string{"preflight", "review", "approve"} {
		t.Run(action, func(t *testing.T) {
			f := newCorrectionHTTPFixture(t)
			rid := prepareCorrectionFailure(t, f)
			aid, sha := f.upload(t, fixedCorrectionFiles(), "audit-failure")
			reviews := 0
			pref := ""
			if action == "approve" {
				review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
				pref = review["id"].(string)
				reviews = 1
			}
			auditAction := "oracle.correction." + action
			if action == "preflight" {
				auditAction = "migration.preflight"
			}
			var beforeCount int
			if err := f.f.db.SQL.QueryRow("SELECT count(*) FROM audit_events WHERE action=?", auditAction).Scan(&beforeCount); err != nil {
				t.Fatal(err)
			}
			_, err := f.f.db.SQL.Exec("CREATE TRIGGER reject_correction_audit BEFORE INSERT ON audit_events WHEN NEW.action='" + auditAction + "' BEGIN SELECT RAISE(ABORT,'private-driver-detail'); END;")
			if err != nil {
				t.Fatal(err)
			}
			var rec *httptest.ResponseRecorder
			if action != "approve" {
				rec = f.review(t, rid, aid)
			} else {
				rec = f.approve(t, pref, sha)
			}
			assertProblemCode(t, rec, 500, "AUDIT_FAILURE")
			assertCorrectionHeld(t, f, aid, reviews)
			if strings.Contains(rec.Body.String(), "private-driver-detail") {
				t.Fatal("internal audit error leaked")
			}
			var n int
			if err = f.f.db.SQL.QueryRow("SELECT count(*) FROM audit_events WHERE action=?", auditAction).Scan(&n); err != nil || n != beforeCount {
				t.Fatal("false success audit", err)
			}
			if err = f.f.db.SQL.QueryRow("SELECT count(*) FROM audit_events WHERE action='request.denied'").Scan(&n); err != nil || n == 0 {
				t.Fatal("rollback did not release connection for failure audit", err)
			}
		})
	}
}

func TestCorrectionInsufficientEvidenceNeverVerifies(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, _ := f.upload(t, fixedCorrectionFiles(), "insufficient")
	f.runner.mu.Lock()
	f.runner.evidenceHook = func(context.Context) error { return errors.New("private-password ORA-00942") }
	f.runner.mu.Unlock()
	rec := f.review(t, rid, aid)
	assertProblemCode(t, rec, 409, "ORACLE_EVIDENCE_UNAVAILABLE")
	assertCorrectionHeld(t, f, aid, 0)
	if strings.Contains(rec.Body.String(), "private-password") {
		t.Fatal("probe error leaked secret")
	}
}

func TestCorrectionApprovalDenialPreservesHoldAndAudit(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, sha := f.upload(t, fixedCorrectionFiles(), "approval-denial")
	review := decodeData[map[string]any](t, f.review(t, rid, aid), 200)
	body := core.CorrectionApprovalInput{PreflightID: review["id"].(string), InputSHA256: sha, PartialDDLReviewed: true, Note: "Independent fixture review"}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", fmt.Sprintf("http://manager.test/api/v2/projects/%s/deployment-targets/%s/correction-approvals", f.d.ProjectID, f.d.ID), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.f.handler.ServeHTTP(rec, req)
	assertProblemCode(t, rec, 403, "FORBIDDEN")
	body.PartialDDLReviewed = false
	assertProblemCode(t, correctionRequest(f, context.Background(), "correction-approvals", body), 400, "VALIDATION_ERROR")
	body.PartialDDLReviewed = true
	body.InputSHA256 = strings.Repeat("0", 64)
	assertProblemCode(t, correctionRequest(f, context.Background(), "correction-approvals", body), 409, "CONFLICT")
	if err := f.f.service.SetGrants(context.Background(), f.tr, "user", f.f.admin.ID, []core.Grant{{TargetID: f.d.TargetID, SchemaID: f.d.SchemaID, Read: true}}); err != nil {
		t.Fatal(err)
	}
	body.InputSHA256 = sha
	assertProblemCode(t, correctionRequest(f, context.Background(), "correction-approvals", body), 403, "FORBIDDEN")
	assertCorrectionHeld(t, f, aid, 1)
	var denied int
	if err := f.f.db.SQL.QueryRow("SELECT count(*) FROM audit_events WHERE action='request.denied'").Scan(&denied); err != nil || denied < 4 {
		t.Fatal("approval denial was not recorded", denied, err)
	}
}

func TestCorrectionCancelledEvidenceReturnsCancellationWithoutApproval(t *testing.T) {
	f := newCorrectionHTTPFixture(t)
	rid := prepareCorrectionFailure(t, f)
	aid, _ := f.upload(t, fixedCorrectionFiles(), "cancelled-review")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.runner.mu.Lock()
	f.runner.evidenceHook = func(context.Context) error { cancel(); return context.Canceled }
	f.runner.mu.Unlock()
	rec := correctionRequest(f, ctx, "correction-reviews", reviewBody(f, rid, aid))
	assertProblemCode(t, rec, 408, "REQUEST_CANCELLED")
	assertCorrectionHeld(t, f, aid, 0)
}
