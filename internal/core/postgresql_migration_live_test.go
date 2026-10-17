package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
)

// This acceptance test runs only with explicit disposable-lab opt-in.
// Candidate qualification used the same worker before policy promotion.
func TestPostgreSQLManagerMigrationLive(t *testing.T) {
	if os.Getenv("FWM_POSTGRES_MIGRATION_LAB") != "1" || !postgresqlMigrationAccepted {
		t.Skip("requires isolated PostgreSQL migration candidate and disposable lab")
	}
	if os.Getenv("PGHOST") != "localhost" || os.Getenv("PGPORT") != "55440" || os.Getenv("PGUSER") != "fwm_exec" {
		t.Fatal("requires named loopback disposable lab")
	}
	dbName := "fwm_manager_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	query := func(t *testing.T, database, sql string) string {
		t.Helper()
		cmd := exec.Command("docker", "exec", "flyway-manager-postgres17-lab", "psql", "-XAtq", "-v", "ON_ERROR_STOP=1", "-U", "lab_admin", "-d", database, "-c", sql)
		b, err := cmd.Output()
		if err != nil {
			if failure, ok := err.(*exec.ExitError); ok {
				t.Fatal("disposable lab observation/setup failed:", strings.TrimSpace(string(failure.Stderr)))
			}
			t.Fatal("disposable lab observation/setup failed", err)
		}
		return strings.TrimSpace(string(b))
	}
	query(t, "postgres", "CREATE DATABASE "+dbName)
	schemas := []string{"success", "independent", "rollback", "partial", "timeout", "shutdown", "process_kill", "unsupported", "changed", "trust", "revoked", "parallel_a", "parallel_b", "policy", "predecessor", "unsafe", "isolation"}
	for _, schema := range schemas {
		query(t, dbName, "CREATE SCHEMA "+schema+" AUTHORIZATION fwm_exec")
	}
	query(t, dbName, "ALTER SCHEMA revoked OWNER TO lab_admin; GRANT USAGE,CREATE ON SCHEMA revoked TO fwm_exec")
	query(t, dbName, "CREATE SCHEMA protected AUTHORIZATION lab_admin; REVOKE ALL ON SCHEMA protected FROM PUBLIC; CREATE TABLE protected.sentinel(id integer PRIMARY KEY, value text); INSERT INTO protected.sentinel VALUES(1,'original')")
	f := newFixture(t)
	ca, err := os.ReadFile(os.Getenv("PGSSLROOTCERT"))
	must(t, err)
	runner := flyway.New(flyway.Config{Executable: os.Getenv("FWM_LAB_FLYWAY"), Version: "13.10.3", SHA256: "a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c", TimeoutSeconds: 12}, filepath.Join(f.dir, "runtime"))
	must(t, f.s.StartExecution(testCtx, runner, "postgresql-manager-lifecycle-v1", f.dir, artifact.DefaultLimits()))
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			f.s.StopExecution()
		}
	})
	in := ConnectionInput{Target: TargetInput{Engine: "postgresql", Name: "Disposable Manager acceptance", Environment: "DEV", Host: "localhost", Port: 55440, DatabaseName: dbName, Transport: "TCPS", Profile: &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: string(ca)}}, Username: "fwm_exec", Password: os.Getenv("PGPASSWORD"), Schemas: schemas, GrantCurrentUser: true}
	saved, err := f.s.SaveConnection(testCtx, f.trace(), "", in)
	must(t, err)
	byName := map[string]Schema{}
	for _, s := range saved.Schemas {
		byName[s.Name] = s
	}
	evidence := map[string]any{"database": dbName, "server_version": query(t, dbName, "SHOW server_version"), "disposable_lab_only": true}
	evidence["before_schema_objects"] = json.RawMessage(query(t, dbName, "SELECT json_object_agg(n.nspname,(SELECT count(*) FROM pg_class c WHERE c.relnamespace=n.oid)) FROM pg_namespace n WHERE n.nspname IN ('"+strings.Join(schemas, "','")+"')"))
	t.Cleanup(func() {
		evidence["test_failed"] = t.Failed()
		if out := os.Getenv("FWM_LAB_EVIDENCE"); out != "" {
			b, e := json.MarshalIndent(evidence, "", "  ")
			if e != nil || (in.Password != "" && strings.Contains(string(b), in.Password)) {
				t.Error("unsafe acceptance evidence")
				return
			}
			if e = os.MkdirAll(out, 0700); e == nil {
				e = os.WriteFile(filepath.Join(out, "manager-migration.json"), b, 0600)
			}
			if e != nil {
				t.Error(e)
			}
		}
	})
	upload := func(t *testing.T, schema string, files map[string]string) RunInput {
		t.Helper()
		inputs := []artifact.Input{}
		for name, sql := range files {
			inputs = append(inputs, artifact.Input{Name: name, Bytes: []byte(sql)})
		}
		a, e := f.s.UploadArtifact(testCtx, f.trace(), saved.Target.ID, byName[schema].ID, inputs)
		must(t, e)
		return RunInput{TargetID: saved.Target.ID, SchemaID: byName[schema].ID, ExecutionAccountID: saved.Account.ID, ArtifactID: a.ID}
	}
	wait := func(t *testing.T, rid string) Run {
		t.Helper()
		deadline := time.Now().Add(150 * time.Second)
		for time.Now().Before(deadline) {
			r, e := f.s.Run(testCtx, f.admin, rid)
			must(t, e)
			if r.Status != "QUEUED" && r.Status != "PREFLIGHT" && r.Status != "RUNNING" && r.Status != "REPAIRING" {
				return r
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("Manager acceptance run did not finish")
		return Run{}
	}
	record := func(t *testing.T, name string, run Run) {
		t.Helper()
		executions, e := f.s.RunExecutions(testCtx, f.admin, run.ID)
		must(t, e)
		logs, e := f.s.RunLogs(testCtx, f.admin, run.ID, Page{Limit: 1000}, "")
		must(t, e)
		files, e := f.s.RunFiles(testCtx, f.admin, run.ID)
		must(t, e)
		entry := map[string]any{"run": run, "executions": executions, "logs": logs, "files": files}
		evidence[name] = entry
		database := run.Target.DatabaseName
		objects := query(t, database, "SELECT COALESCE(json_agg(c.relname||':'||c.relkind::text ORDER BY c.relname),'[]') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='"+run.Schema.Name+"'")
		history := "null"
		if query(t, database, "SELECT to_regclass('"+run.Schema.Name+".flyway_schema_history') IS NOT NULL") == "t" {
			history = query(t, database, "SELECT COALESCE(json_agg(h ORDER BY installed_rank),'[]') FROM "+run.Schema.Name+".flyway_schema_history h")
		}
		entry["database_objects"] = json.RawMessage(objects)
		entry["database_history"] = json.RawMessage(history)
		for _, ex := range executions {
			if ex.Command == "repair" {
				t.Fatal("PostgreSQL attempted Repair")
			}
		}
	}
	submit := func(t *testing.T, input RunInput, key string) Run {
		t.Helper()
		r, e := f.s.SubmitRun(testCtx, f.trace(), input, key)
		must(t, e)
		return r
	}
	base := map[string]string{"V1__base.sql": "CREATE TABLE rows(id integer PRIMARY KEY); INSERT INTO rows VALUES(1);", "V2__second.sql": "INSERT INTO rows VALUES(2);", "R__count.sql": "CREATE OR REPLACE VIEW row_count AS SELECT count(*) AS n FROM rows;"}
	t.Run("nontransactional-maintenance-admission", func(t *testing.T) {
		forms := []string{"ALTER TABLE t DETACH PARTITION p CONCURRENTLY", "CLUSTER", "CLUSTER partitioned_table", "ALTER SUBSCRIPTION s REFRESH PUBLICATION", "ALTER SUBSCRIPTION s SET PUBLICATION p"}
		for i, sql := range forms {
			input := upload(t, "unsupported", map[string]string{"V1__maintenance.sql": sql})
			pre, e := f.s.Preflight(testCtx, f.trace(), input)
			must(t, e)
			if pre.Ready || len(pre.Blockers) == 0 || pre.Blockers[0].Code != "UNSUPPORTED_MIGRATION_SQL" {
				t.Fatal("maintenance SQL bypassed real Manager admission", sql, pre.Blockers)
			}
			_, e = f.s.SubmitRun(testCtx, f.trace(), input, fmt.Sprintf("maintenance-%d", i))
			status(t, e, 409)
		}
		var jobs, commands int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_runs").Scan(&jobs))
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions").Scan(&commands))
		objects := query(t, dbName, "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='unsupported'")
		if jobs != 0 || commands != 0 || objects != "0" {
			t.Fatal("unsupported maintenance created a job, command or database object", jobs, commands, objects)
		}
		evidence["maintenance-admission"] = map[string]any{"rejected_forms": forms, "jobs": jobs, "commands": commands, "schema_objects": objects}
	})
	t.Run("database-execution-policy", func(t *testing.T) {
		input := upload(t, "policy", map[string]string{"V1__policy.sql": "CREATE TABLE observed_policy AS SELECT current_setting('statement_timeout') AS statement_timeout, current_setting('transaction_timeout') AS transaction_timeout, current_setting('lock_timeout') AS lock_timeout, current_setting('idle_session_timeout') AS idle_session_timeout, current_setting('idle_in_transaction_session_timeout') AS idle_transaction_timeout, current_setting('standard_conforming_strings') AS standard_conforming_strings, current_setting('application_name') AS application_name, pg_backend_pid() AS backend_pid;"})
		final := wait(t, submit(t, input, "observed-policy").ID)
		record(t, "observed-policy", final)
		if final.Status != "SUCCEEDED" {
			t.Fatal("policy fixture did not execute", final.Reason)
		}
		settings := query(t, dbName, "SELECT row_to_json(p) FROM policy.observed_policy p")
		evidence["database-observed-policy"] = json.RawMessage(settings)
		var observed map[string]any
		must(t, json.Unmarshal([]byte(settings), &observed))
		for _, name := range []string{"statement_timeout", "transaction_timeout", "idle_session_timeout", "idle_transaction_timeout"} {
			if observed[name] != "12s" {
				t.Fatal("database-side execution bound differs from pinned policy", name, observed[name])
			}
		}
		if observed["lock_timeout"] != "5s" || observed["standard_conforming_strings"] != "on" || !strings.HasPrefix(observed["application_name"].(string), "fwm_") {
			t.Fatal("database lock/tag policy was not applied", observed)
		}
		if final.FlywayConfiguration["postgresql.transactionTimeoutMs"] != "12000" {
			t.Fatal("runtime bounds were not frozen in accepted snapshot")
		}
	})
	t.Run("predecessor-failure-blocks-queued", func(t *testing.T) {
		firstInput := upload(t, "predecessor", map[string]string{"V1__committed.sql": "CREATE TABLE committed(id int PRIMARY KEY); INSERT INTO committed VALUES(1);", "V2__fail.sql": "SELECT pg_sleep(4); CREATE TABLE transient(id int); INSERT INTO missing_table VALUES(1);"})
		secondInput := upload(t, "predecessor", map[string]string{"V3__must_not_execute.sql": "CREATE TABLE must_not_execute(id int);"})
		first := submit(t, firstInput, "predecessor-first")
		second := submit(t, secondInput, "predecessor-second")
		failed := wait(t, first.ID)
		blocked := wait(t, second.ID)
		record(t, "predecessor-failed", failed)
		record(t, "predecessor-blocked", blocked)
		if failed.Status != "NEEDS_REVIEW" || blocked.Status != "BLOCKED" {
			t.Fatal("failed predecessor did not hold/block successor", failed.Status, blocked.Status)
		}
		if query(t, dbName, "SELECT count(*) FROM predecessor.committed") != "1" || query(t, dbName, "SELECT to_regclass('predecessor.must_not_execute') IS NULL") != "t" {
			t.Fatal("predecessor SQL was replayed or queued successor executed")
		}
		var mutations int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE run_id=? AND command IN ('migrate','repair')", second.ID).Scan(&mutations))
		if mutations != 0 {
			t.Fatal("blocked successor dispatched SQL")
		}
	})
	t.Run("native-unsafe-history-blocks-worker", func(t *testing.T) {
		input := upload(t, "unsafe", map[string]string{"V2__pending.sql": "CREATE TABLE must_not_execute(id int);"})
		plan, e := f.s.localPlan(testCtx, f.db.SQL, f.admin, input, "13.10.3", true)
		must(t, e)
		fixture := requestFor(plan, "migrate")
		fixture.Files = map[string][]byte{"V1__external_fixture.sql": []byte("CREATE TABLE external_fixture(id int);")}
		// Native fixture setup is not Manager migration acceptance. The actual
		// preflight/worker below must reject its unknown successful history.
		applied := runner.Execute(testCtx, fixture)
		clear(plan.password)
		if !applied.Success {
			t.Fatal("native unsafe-history fixture setup failed", applied.Stderr)
		}
		evidence["unsafe-history-fixture-only"] = applied
		pre, e := f.s.Preflight(testCtx, f.trace(), input)
		must(t, e)
		if pre.Ready || len(pre.Blockers) == 0 || pre.Blockers[0].Code != "HISTORY_UNSAFE" {
			t.Fatal("real native missing history bypassed Manager preflight", pre.Blockers)
		}
		final := wait(t, submit(t, input, "unsafe-history").ID)
		record(t, "native-unsafe-history", final)
		if final.Status != "BLOCKED" || query(t, dbName, "SELECT to_regclass('unsafe.must_not_execute') IS NULL") != "t" {
			t.Fatal("real native unsafe history bypassed worker", final.Status)
		}
	})
	t.Run("versioned-repeatable-idempotency", func(t *testing.T) {
		input := upload(t, "success", base)
		pre, e := f.s.Preflight(testCtx, f.trace(), input)
		must(t, e)
		if !pre.Ready {
			t.Fatalf("candidate preflight blocked: %+v", pre.Blockers)
		}
		r := submit(t, input, "success")
		duplicate := submit(t, input, "success")
		if duplicate.ID != r.ID {
			t.Fatal("duplicate submission created second run")
		}
		final := wait(t, r.ID)
		record(t, "success", final)
		if final.Status != "SUCCEEDED" {
			t.Fatalf("Manager run %s: %s", final.Status, final.Reason)
		}
		if n := query(t, dbName, "SELECT count(*) FROM success.flyway_schema_history WHERE success"); n != "3" {
			t.Fatal("missing V1/V2/repeatable history", n)
		}
		if n := query(t, dbName, "SELECT n FROM success.row_count"); n != "2" {
			t.Fatal("incorrect rows", n)
		}
		second := wait(t, submit(t, input, "success-second").ID)
		record(t, "no-replay", second)
		if second.Status != "SUCCEEDED" || query(t, dbName, "SELECT count(*) FROM success.flyway_schema_history") != "3" {
			t.Fatal("identical later run replayed or failed")
		}
		// SQL Viewer returns the immutable bytes from the actual accepted run.
		files, e := f.s.RunFiles(testCtx, f.admin, final.ID)
		must(t, e)
		for _, file := range files {
			_, b, e := f.s.RunFileBytes(testCtx, f.admin, final.ID, file.ArtifactID, file.Path)
			must(t, e)
			if string(b) != base[file.Path] {
				t.Fatal("immutable SQL Viewer changed accepted bytes")
			}
		}
	})
	if t.Failed() {
		return
	}
	t.Run("independent-schema", func(t *testing.T) {
		r := wait(t, submit(t, upload(t, "independent", base), "independent").ID)
		record(t, "independent-schema", r)
		if r.Status != "SUCCEEDED" || query(t, dbName, "SELECT count(*) FROM independent.rows") != "2" || query(t, dbName, "SELECT count(*) FROM success.rows") != "2" {
			t.Fatal("schema isolation failed", r.Reason)
		}
	})
	t.Run("rollback-and-partial-success-held", func(t *testing.T) {
		for _, partial := range []bool{false, true} {
			schema := "rollback"
			files := map[string]string{"V1__fail.sql": "CREATE TABLE transient(id int); INSERT INTO nonexistent VALUES(1);"}
			if partial {
				schema = "partial"
				files = map[string]string{"V1__committed.sql": "CREATE TABLE committed(id int PRIMARY KEY); INSERT INTO committed VALUES(1);", "V2__fail.sql": "CREATE TABLE transient(id int); INSERT INTO committed VALUES(1);"}
			}
			input := upload(t, schema, files)
			r := wait(t, submit(t, input, schema).ID)
			record(t, schema, r)
			if r.Status != "NEEDS_REVIEW" || r.RepairOutcome != "SKIPPED" {
				t.Fatal("PostgreSQL failure not conservatively held", r.Status, r.Reason)
			}
			if query(t, dbName, fmt.Sprintf("SELECT to_regclass('%s.transient') IS NULL", schema)) != "t" {
				t.Fatal("failed-script transaction survived")
			}
			if partial && query(t, dbName, "SELECT count(*) FROM partial.committed") != "1" {
				t.Fatal("earlier committed script not observed")
			}
			var held bool
			must(t, f.db.SQL.QueryRow("SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?", input.TargetID, input.SchemaID).Scan(&held))
			if !held {
				t.Fatal("failed stream released")
			}
			_, e := f.s.SubmitRun(testCtx, f.trace(), input, schema+"-retry")
			status(t, e, 409)
		}
	})
	t.Run("timeout-and-backend-observation", func(t *testing.T) {
		input := upload(t, "timeout", map[string]string{"V1__timeout.sql": "CREATE TABLE transient(id int); SELECT pg_sleep(90); INSERT INTO transient VALUES(1);"})
		r := submit(t, input, "timeout")
		deadline := time.Now().Add(90 * time.Second)
		active := ""
		for time.Now().Before(deadline) {
			active = query(t, dbName, "SELECT pid||':'||state||':'||COALESCE(xact_start::text,'')||':'||application_name FROM pg_stat_activity WHERE datname=current_database() AND application_name LIKE 'fwm_%' AND query LIKE '%pg_sleep(90)%' AND state='active'")
			if active != "" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if active == "" {
			t.Fatal("timeout fixture never observed active")
		}
		evidence["timeout-active-before"] = active
		final := wait(t, r.ID)
		record(t, "timeout", final)
		evidence["timeout-after"] = query(t, dbName, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name LIKE 'fwm_%'")
		if final.Status != "NEEDS_REVIEW" || evidence["timeout-after"] != "0" || query(t, dbName, "SELECT to_regclass('timeout.transient') IS NULL") != "t" {
			t.Fatal("timeout did not stop/rollback failed statement and hold run", final.Reason)
		}
	})
	t.Run("manager-cli-process-group-kill", func(t *testing.T) {
		input := upload(t, "process_kill", map[string]string{
			"V1__committed.sql": "CREATE TABLE committed(id int PRIMARY KEY); INSERT INTO committed VALUES(1);",
			"V2__killed.sql":    "CREATE TABLE transient(id int); SELECT pg_sleep(90); INSERT INTO committed VALUES(2);",
		})
		r := submit(t, input, "process-group-kill")
		if r.FlywayConfiguration["postgresql.transactionTimeoutMs"] != "12000" {
			t.Fatal("process-group fixture did not freeze the 12 second server transaction bound", r.FlywayConfiguration)
		}
		deadline := time.Now().Add(60 * time.Second)
		var tag string
		for time.Now().Before(deadline) {
			var message string
			e := f.db.SQL.QueryRow("SELECT message FROM migration_logs WHERE run_id=? AND command='migrate' AND message LIKE 'PostgreSQL execution correlation: %' ORDER BY sequence DESC LIMIT 1", r.ID).Scan(&message)
			if e == nil {
				tag = strings.TrimPrefix(message, "PostgreSQL execution correlation: ")
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if tag == "" {
			t.Fatal("Manager did not durably record the migration correlation tag")
		}
		type guardEvent struct {
			Event             string `json:"event"`
			ApplicationName   string `json:"application_name"`
			CLIPID            int    `json:"cli_pid"`
			CLIProcessGroupID int    `json:"cli_process_group_id"`
		}
		type lifecycleJournal struct {
			Events []guardEvent `json:"events"`
		}
		journal := lifecycleJournal{}
		pid, pgid := 0, 0
		active := map[string]any{}
		for time.Now().Before(deadline) {
			raw, readErr := runner.ReadPostgreSQLLifecycle(tag)
			if readErr == nil {
				if json.Unmarshal(raw, &journal) != nil {
					t.Fatal("Manager lifecycle journal was malformed")
				}
				for _, event := range journal.Events {
					if event.Event == "cli_started" && event.ApplicationName == tag {
						pid, pgid = event.CLIPID, event.CLIProcessGroupID
					}
				}
			}
			queryText := "SELECT row_to_json(x) FROM (SELECT pid,backend_start,state,application_name,xact_start,query_start,wait_event_type,wait_event,clock_timestamp() AS observed_at,query FROM pg_stat_activity WHERE datname=current_database() AND usename='fwm_exec' AND application_name='" + tag + "' AND state='active' AND query LIKE '%pg_sleep(90)%' LIMIT 1) x"
			observation := query(t, dbName, queryText)
			if observation != "" {
				must(t, json.Unmarshal([]byte(observation), &active))
				if pid > 0 && pgid > 0 {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(active) == 0 || pid <= 1 || pgid != pid {
			t.Fatal("active V2 backend or verified CLI process-group identity was not observed", active, pid, pgid)
		}
		backendPID := int(active["pid"].(float64))
		xactStartText := active["xact_start"].(string)
		xactStart, e := time.Parse(time.RFC3339Nano, xactStartText)
		must(t, e)
		initialAge, e := time.ParseDuration(fmt.Sprintf("%ss", query(t, dbName, "SELECT EXTRACT(EPOCH FROM (clock_timestamp()-TIMESTAMPTZ '"+xactStartText+"'))::text")))
		must(t, e)
		if initialAge >= 12*time.Second {
			t.Fatal("process-group signal observation arrived after the frozen transaction budget", initialAge)
		}
		backendDeadline := time.Now().Add(13*time.Second - initialAge)
		// Verify the journal-owned process still has the recorded private group.
		ps := exec.Command("ps", "-o", "pgid=,args=", "-p", strconv.Itoa(pid))
		psOutput, e := ps.Output()
		must(t, e)
		psFields := strings.Fields(string(psOutput))
		if len(psFields) < 2 || psFields[0] != strconv.Itoa(pgid) {
			t.Fatal("journal PID no longer identifies its recorded Flyway process group", string(psOutput), pid, pgid)
		}
		killAt := time.Now()
		if output, killErr := exec.Command("kill", "-KILL", "--", "-"+strconv.Itoa(pgid)).CombinedOutput(); killErr != nil {
			t.Fatalf("could not signal owned Flyway process group %d: %v (%s)", pgid, killErr, output)
		}
		backendGoneAt := time.Time{}
		for time.Now().Before(backendDeadline) {
			remaining := query(t, dbName, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename='fwm_exec' AND application_name='"+tag+"'")
			if remaining == "0" {
				backendGoneAt = time.Now()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if backendGoneAt.IsZero() {
			t.Fatal("backend remained active beyond the frozen transaction timeout observation window")
		}
		serverElapsed := time.Duration(0)
		elapsedText := query(t, dbName, "SELECT EXTRACT(EPOCH FROM (clock_timestamp()-TIMESTAMPTZ '"+xactStartText+"'))::text")
		serverElapsed, e = time.ParseDuration(fmt.Sprintf("%ss", elapsedText))
		must(t, e)
		if serverElapsed > 13*time.Second || time.Since(killAt) > 13*time.Second {
			t.Fatal("backend disappearance exceeded the 12 second bound plus one second observation tolerance", serverElapsed, time.Since(killAt))
		}
		evidence["process-group-cancellation-observations"] = map[string]any{
			"run_id": r.ID, "execution_id": strings.TrimPrefix(tag, "fwm_"),
			"transaction_budget_seconds": 12, "observation_tolerance_seconds": 1,
			"application_name": tag, "backend_pid": backendPID, "backend_before": active,
			"cli_pid": pid, "cli_process_group_id": pgid, "xact_start": xactStart,
			"kill_at": killAt.UTC(), "backend_gone_at": backendGoneAt.UTC(),
			"server_elapsed_from_xact_start": serverElapsed.String(), "disappearance_after_signal": backendGoneAt.Sub(killAt).String(),
		}
		final := wait(t, r.ID)
		record(t, "process-group-cancellation", final)
		if final.Status != "NEEDS_REVIEW" || query(t, dbName, "SELECT count(*) FROM process_kill.committed") != "1" || query(t, dbName, "SELECT to_regclass('process_kill.transient') IS NULL") != "t" || query(t, dbName, "SELECT count(*) FROM process_kill.flyway_schema_history WHERE version='1' AND success") != "1" || query(t, dbName, "SELECT count(*) FROM process_kill.flyway_schema_history WHERE version='2'") != "0" {
			t.Fatal("process-group termination did not preserve committed V1, roll back V2, and hold the run", final.Status, final.Reason)
		}
		var held bool
		must(t, f.db.SQL.QueryRow("SELECT held FROM migration_streams WHERE target_id=? AND schema_id=?", input.TargetID, input.SchemaID).Scan(&held))
		if !held {
			t.Fatal("process-group terminated migration stream was released")
		}
		executions, e := f.s.RunExecutions(testCtx, f.admin, r.ID)
		must(t, e)
		migrations := 0
		for _, execution := range executions {
			if execution.Command == "migrate" {
				migrations++
			}
			if execution.Command == "repair" {
				t.Fatal("process-group terminated migration attempted Repair")
			}
		}
		if migrations != 1 {
			t.Fatal("process-group termination caused replay", migrations)
		}
		_, e = f.s.SubmitRun(testCtx, f.trace(), input, "process-group-kill-retry")
		status(t, e, 409)
	})
	t.Run("unsupported-sql-and-immutable-checksum", func(t *testing.T) {
		input := upload(t, "unsupported", map[string]string{"V1__concurrent.sql": "CREATE INDEX CONCURRENTLY i ON nonexistent(id);"})
		p, e := f.s.Preflight(testCtx, f.trace(), input)
		must(t, e)
		if p.Ready || len(p.Blockers) == 0 || p.Blockers[0].Code != "UNSUPPORTED_MIGRATION_SQL" {
			t.Fatal("unsupported nontransactional SQL was admitted", p.Blockers)
		}
		_, e = f.s.SubmitRun(testCtx, f.trace(), input, "unsupported")
		status(t, e, 409)
		var jobs int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_runs WHERE schema_id=?", input.SchemaID).Scan(&jobs))
		if jobs != 0 {
			t.Fatal("unsupported SQL created a job")
		}
		// The immutable stream rejects checksum drift before native dispatch.
		drift := upload(t, "success", map[string]string{"V1__base.sql": base["V1__base.sql"] + " -- checksum drift"})
		p, e = f.s.Preflight(testCtx, f.trace(), drift)
		must(t, e)
		if p.Ready {
			t.Fatal("changed versioned checksum admitted")
		}
		var beforeRuns, beforeMigrations int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_runs WHERE schema_id=?", drift.SchemaID).Scan(&beforeRuns))
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE command='migrate'").Scan(&beforeMigrations))
		_, e = f.s.SubmitRun(testCtx, f.trace(), drift, "changed-checksum")
		status(t, e, 409)
		var afterRuns, afterMigrations int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_runs WHERE schema_id=?", drift.SchemaID).Scan(&afterRuns))
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE command='migrate'").Scan(&afterMigrations))
		if afterRuns != beforeRuns || afterMigrations != beforeMigrations || query(t, dbName, "SELECT count(*) FROM success.rows") != "2" {
			t.Fatal("changed immutable checksum created a run, dispatched SQL, or changed database state")
		}
		// A truncated ZIP is rejected before an artifact or run is recorded.
		var beforeArtifacts, beforeArchiveRuns int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM artifacts WHERE target_id=? AND schema_id=?", drift.TargetID, drift.SchemaID).Scan(&beforeArtifacts))
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_runs WHERE schema_id=?", drift.SchemaID).Scan(&beforeArchiveRuns))
		_, e = f.s.UploadArtifact(testCtx, f.trace(), drift.TargetID, drift.SchemaID, []artifact.Input{{Name: "broken.zip", Bytes: []byte("PK\x03\x04truncated")}})
		if e == nil {
			t.Fatal("truncated archive was accepted")
		}
		var afterArtifacts, afterArchiveRuns int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM artifacts WHERE target_id=? AND schema_id=?", drift.TargetID, drift.SchemaID).Scan(&afterArtifacts))
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_runs WHERE schema_id=?", drift.SchemaID).Scan(&afterArchiveRuns))
		if afterArtifacts != beforeArtifacts || afterArchiveRuns != beforeArchiveRuns || query(t, dbName, "SELECT count(*) FROM success.rows") != "2" {
			t.Fatal("truncated archive created an artifact/run or changed database state")
		}
		evidence["unsupported-and-checksum"] = map[string]any{"unsupported_jobs": jobs, "checksum_preflight": p, "checksum_submit_status": 409, "database_unchanged": true, "truncated_archive_rejected": true}
	})
	t.Run("concurrent-independent-streams", func(t *testing.T) {
		inputs := []RunInput{upload(t, "parallel_a", map[string]string{"V1__parallel.sql": "SELECT pg_sleep(4); CREATE TABLE once(id int);"}), upload(t, "parallel_b", map[string]string{"V1__parallel.sql": "SELECT pg_sleep(4); CREATE TABLE once(id int);"})}
		type answer struct {
			run Run
			err error
		}
		ch := make(chan answer, 2)
		for i, input := range inputs {
			go func(i int, input RunInput) {
				r, e := f.s.SubmitRun(testCtx, f.trace(), input, fmt.Sprintf("parallel-%d", i))
				ch <- answer{r, e}
			}(i, input)
		}
		runs := []Run{}
		for range inputs {
			a := <-ch
			must(t, a.err)
			runs = append(runs, a.run)
		}
		maxActive := 0
		deadline := time.Now().Add(150 * time.Second)
		for time.Now().Before(deadline) {
			n, e := strconv.Atoi(query(t, dbName, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name LIKE 'fwm_%' AND state='active' AND query LIKE '%pg_sleep(4)%'"))
			must(t, e)
			if n > maxActive {
				maxActive = n
			}
			allDone := true
			for _, r := range runs {
				x, e := f.s.Run(testCtx, f.admin, r.ID)
				must(t, e)
				if x.Status != "SUCCEEDED" && x.Status != "NEEDS_REVIEW" && x.Status != "BLOCKED" {
					allDone = false
				}
			}
			if allDone {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		for i, r := range runs {
			final := wait(t, r.ID)
			record(t, fmt.Sprintf("parallel-%d", i), final)
			if final.Status != "SUCCEEDED" {
				t.Fatal("independent concurrent stream failed", final.Reason)
			}
		}
		evidence["concurrent_active_backends"] = maxActive
		if maxActive < 2 {
			t.Fatal("did not observe independent migration statements concurrently active")
		}
	})
	t.Run("queued-trust-profile-change-rejected", func(t *testing.T) {
		input := upload(t, "trust", map[string]string{"V1__trusted.sql": "CREATE TABLE trusted(id int);"})
		pre, e := f.s.Preflight(testCtx, f.trace(), input)
		must(t, e)
		if !pre.Ready {
			t.Fatal("trust fixture preflight failed", pre.Blockers)
		}
		streamLock := f.s.execution.stream(input.TargetID, input.SchemaID)
		streamLock.Lock()
		r := Run{}
		func() {
			defer streamLock.Unlock()
			r = submit(t, input, "trust-profile")
			var beforeRevision, beforeTargetRevision int
			var beforeHash string
			must(t, f.db.SQL.QueryRow("SELECT revision,ca_sha256 FROM target_connection_profiles WHERE target_id=?", saved.Target.ID).Scan(&beforeRevision, &beforeHash))
			must(t, f.db.SQL.QueryRow("SELECT profile_revision FROM database_targets WHERE id=?", saved.Target.ID).Scan(&beforeTargetRevision))
			changed := in
			changed.Target.Profile = &ConnectionProfile{AuthKind: "password", TLSMode: "verify-full", CAPEM: string(ca) + "\n"}
			_, e = f.s.SaveConnection(testCtx, f.trace(), saved.Target.ID, changed)
			status(t, e, 409)
			var afterRevision, afterTargetRevision int
			var afterHash string
			must(t, f.db.SQL.QueryRow("SELECT revision,ca_sha256 FROM target_connection_profiles WHERE target_id=?", saved.Target.ID).Scan(&afterRevision, &afterHash))
			must(t, f.db.SQL.QueryRow("SELECT profile_revision FROM database_targets WHERE id=?", saved.Target.ID).Scan(&afterTargetRevision))
			if beforeRevision != afterRevision || beforeTargetRevision != afterTargetRevision || beforeHash != afterHash {
				t.Fatal("rejected profile edit changed the saved revision or hash")
			}
		}()
		final := wait(t, r.ID)
		record(t, "rejected-trust-edit", final)
		if final.Status != "SUCCEEDED" || final.RunSnapshot.Profile.CASHA256 != saved.Profile.CASHA256 {
			t.Fatal("rejected trust edit changed the queued contract", final.Status)
		}
		evidence["queued-trust-edit"] = map[string]any{"save_status": 409, "original_ca_sha256": saved.Profile.CASHA256, "queued_ca_sha256": final.RunSnapshot.Profile.CASHA256}
	})
	t.Run("queued-credential-change", func(t *testing.T) {
		input := upload(t, "changed", map[string]string{"V1__changed.sql": "CREATE TABLE must_not_execute(id int);"})
		streamLock := f.s.execution.stream(input.TargetID, input.SchemaID)
		streamLock.Lock()
		r := Run{}
		func() {
			defer streamLock.Unlock()
			r = submit(t, input, "changed")
			_, e := f.s.ReplaceCredential(testCtx, f.trace(), saved.Account.ID, in.Password, false)
			must(t, e)
		}()
		final := wait(t, r.ID)
		record(t, "changed-credential", final)
		if final.Status != "BLOCKED" || query(t, dbName, "SELECT to_regclass('changed.must_not_execute') IS NULL") != "t" {
			t.Fatal("queued credential drift executed", final.Reason)
		}
	})
	t.Run("queued-manager-grant-revoked", func(t *testing.T) {
		input := upload(t, "changed", map[string]string{"V2__operator_must_not_execute.sql": "CREATE TABLE operator_must_not_execute(id int);"})
		user, operator, _ := f.user(t, "postgres-queued-operator", "Operator")
		must(t, f.s.SetGrants(testCtx, f.trace(), "user", user.ID, []Grant{{TargetID: input.TargetID, SchemaID: input.SchemaID, Read: true, Operate: true}}))
		streamLock := f.s.execution.stream(input.TargetID, input.SchemaID)
		streamLock.Lock()
		r := Run{}
		func() {
			defer streamLock.Unlock()
			var e error
			r, e = f.s.SubmitRun(testCtx, Trace{RequestID: "postgres-operator-queue", Actor: operator}, input, "postgres-grant-revoke")
			must(t, e)
			must(t, f.s.SetGrants(testCtx, f.trace(), "user", user.ID, nil))
		}()
		final := wait(t, r.ID)
		record(t, "queued-grant-revoked", final)
		if final.Status != "BLOCKED" || query(t, dbName, "SELECT to_regclass('changed.operator_must_not_execute') IS NULL") != "t" {
			t.Fatal("revoked queued operator dispatched SQL", final.Status, final.Reason)
		}
		var mutations int
		must(t, f.db.SQL.QueryRow("SELECT count(*) FROM run_executions WHERE run_id=? AND command IN ('migrate','repair')", r.ID).Scan(&mutations))
		if mutations != 0 {
			t.Fatal("revoked queued operator produced a mutating execution")
		}
	})
	t.Run("cross-schema-privilege-isolation", func(t *testing.T) {
		if query(t, dbName, "SELECT NOT has_table_privilege('fwm_exec','protected.sentinel','INSERT') AND NOT has_table_privilege('fwm_exec','protected.sentinel','SELECT')") != "t" {
			t.Fatal("protected table fixture accidentally grants table access to the execution role")
		}
		input := upload(t, "isolation", map[string]string{"V1__cross_schema.sql": "INSERT INTO protected.sentinel(id,value) VALUES(2,'unauthorized');"})
		final := wait(t, submit(t, input, "protected-schema-insert").ID)
		record(t, "cross-schema-privilege-isolation", final)
		if final.Status != "NEEDS_REVIEW" || query(t, dbName, "SELECT count(*) FROM protected.sentinel") != "1" || query(t, dbName, "SELECT value FROM protected.sentinel WHERE id=1") != "original" {
			t.Fatal("restricted execution role changed protected cross-schema data", final.Status, final.Reason)
		}
	})
	t.Run("database-permission-revoked", func(t *testing.T) {
		input := upload(t, "revoked", map[string]string{"V1__privilege.sql": "CREATE TABLE forbidden(id int);"})
		query(t, dbName, "REVOKE CREATE ON SCHEMA revoked FROM fwm_exec")
		if query(t, dbName, "SELECT has_schema_privilege('fwm_exec','revoked','CREATE')") != "f" {
			t.Fatal("fixture privilege was not revoked")
		}
		final := wait(t, submit(t, input, "revoked").ID)
		record(t, "revoked", final)
		if final.Status != "NEEDS_REVIEW" || query(t, dbName, "SELECT to_regclass('revoked.forbidden') IS NULL") != "t" {
			t.Fatal("revoked privilege did not fail held", final.Reason)
		}
	})
	t.Run("independent-database", func(t *testing.T) {
		secondDB := dbName + "_second"
		query(t, "postgres", "CREATE DATABASE "+secondDB)
		query(t, secondDB, "CREATE SCHEMA success AUTHORIZATION fwm_exec")
		second := in
		second.Target.DatabaseName = secondDB
		second.Target.Name = "Disposable second database"
		second.Schemas = []string{"success"}
		connection, e := f.s.SaveConnection(testCtx, f.trace(), "", second)
		must(t, e)
		if connection.Schemas[0].ID == byName["success"].ID {
			t.Fatal("cross-database stream ID collision")
		}
		files := []artifact.Input{}
		for name, sql := range base {
			files = append(files, artifact.Input{Name: name, Bytes: []byte(sql)})
		}
		a, e := f.s.UploadArtifact(testCtx, f.trace(), connection.Target.ID, connection.Schemas[0].ID, files)
		must(t, e)
		input := RunInput{TargetID: connection.Target.ID, SchemaID: connection.Schemas[0].ID, ExecutionAccountID: connection.Account.ID, ArtifactID: a.ID}
		final := wait(t, submit(t, input, "second-db").ID)
		record(t, "independent-database", final)
		if final.Status != "SUCCEEDED" || query(t, secondDB, "SELECT count(*) FROM success.rows") != "2" || query(t, dbName, "SELECT count(*) FROM success.rows") != "2" {
			t.Fatal("independent database scope failed", final.Reason)
		}
	})
	t.Run("worker-shutdown-and-restart-held", func(t *testing.T) {
		input := upload(t, "shutdown", map[string]string{"V1__shutdown.sql": "CREATE TABLE transient(id int); SELECT pg_sleep(90);"})
		r := submit(t, input, "shutdown")
		deadline := time.Now().Add(60 * time.Second)
		active := false
		for time.Now().Before(deadline) {
			if query(t, dbName, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name LIKE 'fwm_%' AND state='active' AND query LIKE '%pg_sleep(90)%'") == "1" {
				active = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !active {
			t.Fatal("shutdown migration was not observed active")
		}
		f.s.StopExecution()
		stopped = true
		deadline = time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) && query(t, dbName, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name LIKE 'fwm_%'") != "0" {
			time.Sleep(100 * time.Millisecond)
		}
		if query(t, dbName, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name LIKE 'fwm_%'") != "0" {
			t.Fatal("shutdown backend outlived database timeout bound")
		}
		if query(t, dbName, "SELECT to_regclass('shutdown.transient') IS NULL") != "t" {
			t.Fatal("shutdown transaction effects survived")
		}
		service, e := New(testCtx, f.db, f.dir, "")
		must(t, e)
		f.s = service
		must(t, f.s.StartExecution(testCtx, runner, "postgresql-manager-lifecycle-v1", f.dir, artifact.DefaultLimits()))
		stopped = false
		final := wait(t, r.ID)
		if final.Status != "NEEDS_REVIEW" {
			t.Fatal("shutdown run was retried or released", final.Status)
		}
		deadline = time.Now().Add(5 * time.Second)
		journals := 0
		for time.Now().Before(deadline) {
			must(t, f.db.SQL.QueryRow("SELECT count(*) FROM migration_logs WHERE run_id=? AND message LIKE 'PostgreSQL recovered lifecycle journal:%'", r.ID).Scan(&journals))
			if journals > 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if journals == 0 {
			t.Fatal("restart did not retain durable PostgreSQL lifecycle observations")
		}
		evidence["shutdown-recovered-journal-count"] = journals
		record(t, "shutdown-restart", final)
		_, e = f.s.SubmitRun(testCtx, f.trace(), input, "shutdown-retry")
		status(t, e, 409)
	})
	// The registry is disposable; observations above do not claim product Gate F.
}
