package core

import (
	"encoding/json"
	"strings"
	"testing"

	"flyway-manager/internal/flyway"
)

func TestMigrationFailureIdentityUsesOnlyNativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, payload, info, script, version, source string
		unknown                                      bool
	}{
		{"native-path", `{"error":{"errorCode":"FAILED_VERSIONED_MIGRATION","message":"original error","path":"/private/sql/V42__actual.sql"}}`, "", "V42__actual.sql", "", "migrate", false},
		{"native-message", `{"error":{"errorCode":"FAULT","message":"Migration V42__actual.sql failed\nORA-00942"}}`, "", "V42__actual.sql", "", "migrate", false},
		{"history-only", `{"error":{"errorCode":"FAULT","message":"original error"}}`, `{"migrations":[{"state":"Failed","filepath":"/private/sql/V42__actual.sql","version":"42"}]}`, "V42__actual.sql", "42", "failed_history", false},
		{"unknown", `{"error":{"errorCode":"FAULT","message":"original error"}}`, `{"migrations":[{"state":"Pending","script":"V1__expected.sql","version":"1"}]}`, "", "", "", true},
		{"multiple-failed", `{"error":{"errorCode":"FAULT","message":"original error"}}`, `{"migrations":[{"state":"Failed","script":"V1__one.sql","version":"1"},{"state":"Failed","script":"V2__two.sql","version":"2"}]}`, "", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := migrationFailure("native-id", flyway.Result{JSON: json.RawMessage(tc.payload), Stderr: "original stderr", ExitCode: 9}, json.RawMessage(tc.info))
			if failure.Script != tc.script || failure.Version != tc.version || failure.IdentitySource != tc.source || (failure.IdentityReason != "") != tc.unknown {
				t.Fatalf("invented or lost native identity: %+v", failure)
			}
			if failure.ExecutionID != "native-id" || failure.ExitCode != 9 || failure.ErrorCode == "" || !strings.Contains(failure.Message, "original error") && !strings.Contains(failure.Message, "Migration V42") {
				t.Fatal("original failure lost", failure)
			}
		})
	}
}
