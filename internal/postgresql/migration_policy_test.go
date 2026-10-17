package postgresql

import "testing"

func TestTransactionalMigrationAdmission(t *testing.T) {
	if ValidateMigrationSQL([]byte(`SELECT pg_catalog.U&"set_confi\0067"('statement_timeout','0',false)`)) == nil {
		t.Fatal("Unicode-escaped session control was accepted")
	}
	for _, sql := range []string{"CREATE INDEX /* reason */ CONCURRENTLY i ON t(id);", "CREATE UNIQUE INDEX\nCONCURRENTLY i ON t(id)", "VACUUM t", "REINDEX TABLE t", "CREATE DATABASE other", "SELECT 1;COMMIT;", "SET statement_timeout=0", "SELECT set_config('application_name','other',false)", `SELECT pg_catalog."set_config"('statement_timeout','0',false)`, `SELECT '\'; SET statement_timeout=0;`, "SELECT 1; /*", "DO $$ BEGIN RAISE NOTICE 'unqualified'; END $$", "CALL unknown_procedure()", "CREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE SQL AS 'SELECT 1'", "CREATE SUBSCRIPTION s CONNECTION 'unqualified' PUBLICATION p"} {
		if ValidateMigrationSQL([]byte(sql)) == nil {
			t.Errorf("unsupported SQL accepted: %s", sql)
		}
	}
	for _, sql := range []string{"CREATE TABLE t(id int); INSERT INTO t VALUES(1)", "UPDATE t SET id=2", "SELECT 'VACUUM t; COMMIT'", "-- CREATE INDEX CONCURRENTLY\nCREATE INDEX i ON t(id)", "/* nested /* comment */ ok */ SELECT 1"} {
		if err := ValidateMigrationSQL([]byte(sql)); err != nil {
			t.Errorf("ordinary SQL rejected: %s: %v", sql, err)
		}
	}
}
