package core

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"flyway-manager/internal/flyway"
)

// Frozen by the v7 binary/source path before profile changes. Adding a default
// engine must not invalidate a historical stream or reserialize its snapshot.
func TestOracleV7IdentityBytesRemainFrozen(t *testing.T) {
	b, e := os.ReadFile("testdata/oracle-v7-identity.json")
	must(t, e)
	var frozen struct {
		Snapshot      json.RawMessage `json:"snapshot"`
		Configuration string          `json:"configuration"`
	}
	must(t, json.Unmarshal(b, &frozen))
	var snap RunSnapshot
	must(t, json.Unmarshal(frozen.Snapshot, &snap))
	snap.FlywayConfiguration = flyway.ConfigurationSnapshot(requestFor(executionPlan{snapshot: snap}, "info"))
	actual, e := json.Marshal(snap)
	must(t, e)
	var compact bytes.Buffer
	must(t, json.Compact(&compact, frozen.Snapshot))
	if string(actual) != compact.String() || streamConfiguration(snap) != frozen.Configuration {
		t.Fatal("Oracle snapshot or stream configuration changed")
	}
	f := newFixture(t)
	target := f.target(t, "legacy", "DEV")
	targetJSON, _ := json.Marshal(target)
	if strings.Contains(string(targetJSON), "engine") || strings.Contains(string(targetJSON), "profile_revision") || strings.Contains(string(targetJSON), "database_name") {
		t.Fatal("Oracle default entered legacy snapshot")
	}
}
func TestEngineTargetValidationFailsClosed(t *testing.T) {
	base := TargetInput{Name: "PG", Environment: "DEV", Host: "localhost", Port: 5432, Engine: "postgresql", DatabaseName: "app_db", Transport: "TCPS"}
	_, e := normalizeTarget("pg", base)
	must(t, e)
	for _, edit := range []func(*TargetInput){func(x *TargetInput) { x.Engine = "mysql" }, func(x *TargetInput) { x.DatabaseName = "APP" }, func(x *TargetInput) { x.DatabaseName = "a?sslmode=disable" }, func(x *TargetInput) { x.Service = "fake" }, func(x *TargetInput) { x.Transport = "TCP" }, func(x *TargetInput) { x.DatabaseName = strings.Repeat("a", 64) }} {
		in := base
		edit(&in)
		_, e := normalizeTarget("pg", in)
		status(t, e, 400)
	}
	for _, name := range []string{"a", "_app", "app_1"} {
		if !validDBName("postgresql", name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"APP", "a-b", "a b", "a.b", "a\"", ""} {
		if validDBName("postgresql", name) {
			t.Fatal(name)
		}
	}
}
