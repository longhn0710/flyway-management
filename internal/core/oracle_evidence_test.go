package core

import (
	"context"
	"encoding/json"
	"flyway-manager/internal/artifact"
	"flyway-manager/internal/flyway"
	"flyway-manager/internal/oracle"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Physical rows are independently represented from the fixture's Flyway JSON.
func (f *fakeEngine) OracleEvidence(_ context.Context, r flyway.Request) (oracle.Evidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := oracle.Evidence{DBID: "1514744704", DatabaseUniqueName: "fixture", PDBName: r.Service, Service: r.Service, SessionUser: r.Username, CurrentSchema: r.Schema, ObjectsSHA256: digest("fixture objects"), History: []oracle.HistoryRow{}}
	names := []string{}
	for p, applied := range f.applied[r.Schema] {
		if applied {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	if failed := f.failed[r.Schema]; failed != "" {
		names = append(names, failed)
	}
	for i, p := range names {
		version := ""
		if strings.HasPrefix(p, "V") {
			version = strings.TrimPrefix(strings.Split(p, "__")[0], "V")
		}
		checksum := oracleSQLChecksum(r.Files[p])
		e.History = append(e.History, oracle.HistoryRow{Rank: i + 1, Version: version, Description: p, Type: "SQL", Script: p, Checksum: &checksum, InstalledBy: r.Username, InstalledOn: "2026-10-16T00:00:00Z", Success: f.failed[r.Schema] != p})
	}
	return e, nil
}

func TestOraclePhysicalRepairRejectsUnreportedRowsAndChecksumDrift(t *testing.T) {
	crc := oracleSQLChecksum([]byte("bad sql"))
	row := oracle.HistoryRow{Rank: 1, Version: "6", Script: "V6__bad.sql", Checksum: &crc, Success: false}
	baseline := oracle.Evidence{DBID: "1", DatabaseUniqueName: "lab", PDBName: "pdb", Service: "svc", SessionUser: "APP", CurrentSchema: "APP", History: []oracle.HistoryRow{}}
	failed := baseline
	failed.History = []oracle.HistoryRow{row}
	file := storedFile{File: artifact.File{Path: "V6__bad.sql", Kind: "versioned", Version: "6", Bytes: []byte("bad sql")}}
	failure := &MigrationFailure{Version: "6", Script: "V6__bad.sql"}
	native := json.RawMessage(`{"migrationsExecuted":0,"migrations":[]}`)
	if !physicalRepairCandidate(baseline, failed, failure, []storedFile{file}, native) {
		t.Fatal("single confirmed failed row rejected")
	}
	unexpected := failed
	unexpected.History = append([]oracle.HistoryRow{row}, oracle.HistoryRow{Rank: 2, Version: "99", Script: "V99__foreign.sql", Success: true})
	if physicalRepairCandidate(baseline, unexpected, failure, []storedFile{file}, native) {
		t.Fatal("unreported successful history accepted")
	}
	wrong := failed
	wrong.History = append([]oracle.HistoryRow{}, failed.History...)
	badCRC := crc + 1
	wrong.History[0].Checksum = &badCRC
	if physicalRepairCandidate(baseline, wrong, failure, []storedFile{file}, native) {
		t.Fatal("physical failed row checksum not bound to frozen SQL")
	}
	if oracleSQLChecksum([]byte("\xef\xbb\xbfline1\r\nline2\r")) != oracleSQLChecksum([]byte("line1\nline2\n")) {
		t.Fatal("Flyway CRC normalization differs")
	}
}

func TestOracleRepairAcceptsUnchangedSupersededRepeatableApplications(t *testing.T) {
	rows := []map[string]any{}
	for i, state := range []string{"Superseded", "Superseded", "Success"} {
		rows = append(rows, map[string]any{"version": "", "script": "R__view.sql", "state": state, "installedOn": fmt.Sprintf("2026-10-1%dT00:00:00Z", i), "type": "SQL"})
	}
	pending := map[string]any{"version": "6", "script": "V6__bad.sql", "state": "Pending", "installedOn": "", "type": "SQL"}
	rows = append(rows, pending)
	before, _ := json.Marshal(map[string]any{"migrations": rows})
	failedRows := append([]map[string]any{}, rows...)
	failedRows[3] = map[string]any{"version": "6", "script": "V6__bad.sql", "state": "Failed", "installedOn": "2026-10-19T01:00:00Z", "type": "SQL"}
	after, _ := json.Marshal(map[string]any{"migrations": failedRows})
	validation := flyway.Result{JSON: json.RawMessage(`{"operation":"validate","flywayVersion":"13.10.3","validationSuccessful":false,"invalidMigrations":[{"version":"6","filepath":"V6__bad.sql","errorDetails":{"errorCode":"FAILED_VERSIONED_MIGRATION"}}]}`)}
	if reason := repairEligibility(flyway.Result{Success: true, Version: "13.10.3", JSON: before}, flyway.Result{Success: true, JSON: after}, validation); reason != "" {
		t.Fatal(reason)
	}
	if !repairResolverRestored(after, before) {
		t.Fatal("unchanged repeated historical applications rejected")
	}
}
