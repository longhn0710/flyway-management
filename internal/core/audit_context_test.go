package core

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestCancelledMutationRollsBackBeforeDetachedFailureAudit(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.SQL.Exec("CREATE TABLE cancellation_fixture(value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := f.s.mutation(ctx, f.trace(), "cancelled.fixture", "fixture", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO cancellation_fixture VALUES('must-rollback')"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	auditCtx, done := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer done()
	if err = f.s.Event(auditCtx, f.trace(), "request.denied", "fixture", "failure"); err != nil {
		t.Fatal("transaction retained sole connection", err)
	}
	var n int
	if err = f.db.SQL.QueryRowContext(auditCtx, "SELECT count(*) FROM cancellation_fixture").Scan(&n); err != nil || n != 0 {
		t.Fatal("canceled mutation committed", n, err)
	}
	if err = f.db.SQL.QueryRowContext(auditCtx, "SELECT count(*) FROM audit_events WHERE action='cancelled.fixture'").Scan(&n); err != nil || n != 0 {
		t.Fatal("canceled mutation success-audited", n, err)
	}
}
