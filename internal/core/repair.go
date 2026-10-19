package core

import (
	"encoding/json"
	"flyway-manager/internal/flyway"
	"fmt"
	"path"
	"reflect"
	"strings"
)

func historyStable(before, after []historyEntry, allowFailed bool) bool {
	old := map[string]historyEntry{}
	for _, r := range before {
		if strings.EqualFold(r.State, "Success") || strings.EqualFold(r.State, "Superseded") {
			old[r.Script+"/"+r.InstalledOn] = r
		}
	}
	current := map[string]historyEntry{}
	for _, r := range after {
		if strings.EqualFold(r.State, "Success") || strings.EqualFold(r.State, "Superseded") {
			current[r.Script+"/"+r.InstalledOn] = r
		} else if !strings.EqualFold(r.State, "Pending") && !strings.EqualFold(r.State, "Outdated") && !(allowFailed && strings.EqualFold(r.State, "Failed")) {
			return false
		}
	}
	return reflect.DeepEqual(old, current)
}

// Repair is allowed only when Flyway itself reports exactly one failed versioned
// migration and no checksum, type, description or missing-migration difference.
func repairEligibility(before, after, validation flyway.Result) string {
	if !before.Success || !after.Success {
		return "Oracle history cannot be inspected reliably; repair is unsafe."
	}
	prior, e := history(before.JSON)
	if e != nil {
		return e.Error()
	}
	next, e := history(after.JSON)
	if e != nil {
		return e.Error()
	}
	for _, entry := range prior {
		if strings.EqualFold(entry.State, "Outdated") {
			return "Outdated repeatable history makes Repair unsafe."
		}
	}
	if !repairHistoryProgress(prior, next) {
		return "Unrelated or ambiguous history changes make repair unsafe."
	}
	failed := []historyEntry{}
	for _, r := range next {
		if strings.EqualFold(r.State, "Failed") {
			failed = append(failed, r)
		}
	}
	if len(failed) != 1 || failed[0].Version == nil || *failed[0].Version == "" || failed[0].Script == "" {
		return "Exactly one failed versioned migration is required for automatic repair."
	}
	candidate := false
	for _, r := range prior {
		if strings.EqualFold(r.State, "Pending") && r.Version != nil && *r.Version == *failed[0].Version && r.Script == failed[0].Script {
			candidate = true
		}
	}
	if !candidate {
		return "The failed history row does not match an originally pending versioned migration."
	}
	var v struct {
		Operation            string `json:"operation"`
		FlywayVersion        string `json:"flywayVersion"`
		ValidationSuccessful *bool  `json:"validationSuccessful"`
		InvalidMigrations    []struct {
			Version      string `json:"version"`
			Filepath     string `json:"filepath"`
			ErrorDetails struct {
				Code string `json:"errorCode"`
			} `json:"errorDetails"`
		} `json:"invalidMigrations"`
	}
	if json.Unmarshal(validation.JSON, &v) != nil || v.Operation != "validate" || v.FlywayVersion != before.Version || v.ValidationSuccessful == nil || *v.ValidationSuccessful || len(v.InvalidMigrations) != 1 {
		return "Flyway validation did not isolate exactly one failed-history error."
	}
	invalid := v.InvalidMigrations[0]
	if invalid.ErrorDetails.Code != "FAILED_VERSIONED_MIGRATION" || invalid.Version != *failed[0].Version {
		return "Flyway reported unrelated validation changes; repair could realign or delete history."
	}
	if invalid.Filepath != "" && path.Base(strings.ReplaceAll(invalid.Filepath, "\\", "/")) != failed[0].Script {
		return "Flyway validation and failed history report different SQL files; repair is unsafe."
	}
	return ""
}
func repairResultSafe(payload json.RawMessage) bool {
	var v struct {
		Removed *[]json.RawMessage `json:"migrationsRemoved"`
		Aligned *[]json.RawMessage `json:"migrationsAligned"`
		Deleted *[]json.RawMessage `json:"migrationsDeleted"`
	}
	return json.Unmarshal(payload, &v) == nil && v.Removed != nil && len(*v.Removed) == 1 && v.Aligned != nil && len(*v.Aligned) == 0 && v.Deleted != nil && len(*v.Deleted) == 0
}
func repairHistoryRestored(before, after json.RawMessage) bool {
	old, e := history(before)
	if e != nil {
		return false
	}
	current, e := history(after)
	if e != nil || safeHistory(after) != nil || !historyStable(old, current, false) || len(old) != len(current) {
		return false
	}
	// The complete pre-migrate resolver view must be restored, including the exact
	// failed candidate becoming Pending again. Missing candidate rows are unsafe.
	canonical := func(rows []historyEntry) map[string]historyEntry {
		out := map[string]historyEntry{}
		for _, row := range rows {
			version := ""
			if row.Version != nil {
				version = *row.Version
			}
			key := version + "/" + row.Script + "/" + row.State + "/" + row.InstalledOn + "/" + fmt.Sprint(row.InstalledRank)
			if _, exists := out[key]; exists {
				return nil
			}
			out[key] = row
		}
		return out
	}
	left, right := canonical(old), canonical(current)
	return left != nil && right != nil && reflect.DeepEqual(left, right)
}

func removedFailedVersion(repaired, failedInfo json.RawMessage) bool {
	rows, e := history(failedInfo)
	if e != nil {
		return false
	}
	version := ""
	for _, row := range rows {
		if strings.EqualFold(row.State, "Failed") && row.Version != nil {
			if version != "" {
				return false
			}
			version = *row.Version
		}
	}
	var result struct {
		Removed []struct {
			Version string `json:"version"`
		} `json:"migrationsRemoved"`
	}
	return version != "" && json.Unmarshal(repaired, &result) == nil && len(result.Removed) == 1 && result.Removed[0].Version == version
}

// Match the entire resolver view after removing only the confirmed failed row.
// Physical checksum-bearing rows are verified separately by the JDBC probe.
func repairResolverRestored(failed, after json.RawMessage) bool {
	rows, err := history(failed)
	if err != nil {
		return false
	}
	count := 0
	for i, r := range rows {
		if strings.EqualFold(r.State, "Failed") {
			count++
			r.State = "Pending"
			r.InstalledOn = ""
			r.InstalledBy = ""
			r.InstalledRank = 0
			r.Checksum = nil
			rows[i] = r
		}
	}
	if count != 1 {
		return false
	}
	raw, _ := json.Marshal(map[string]any{"migrations": rows})
	return repairHistoryRestored(raw, after)
}

func repairHistoryProgress(before, after []historyEntry) bool {
	if len(before) != len(after) {
		return false
	}
	// Multisets preserve every historical application of a repeatable, including
	// multiple Superseded rows for the same script and distinct installation times.
	exact := map[string]int{}
	progress := map[string]historyEntry{}
	canonical := func(r historyEntry) string { raw, _ := json.Marshal(r); return string(raw) }
	for _, r := range after {
		exact[canonical(r)]++
		if r.Version != nil && *r.Version != "" && (r.State == "Success" || r.State == "Failed") {
			key := r.Script + "/" + *r.Version
			if _, ok := progress[key]; ok {
				return false
			}
			progress[key] = r
		}
	}
	for _, r := range before {
		key := canonical(r)
		if exact[key] > 0 {
			exact[key]--
			continue
		}
		if r.State != "Pending" || r.Version == nil || *r.Version == "" {
			return false
		}
		n, ok := progress[r.Script+"/"+*r.Version]
		if !ok {
			return false
		}
		native := canonical(n)
		if exact[native] == 0 {
			return false
		}
		n.State = "Pending"
		n.InstalledOn = ""
		n.InstalledBy = ""
		n.InstalledRank = 0
		n.Checksum = nil
		if !reflect.DeepEqual(r, n) {
			return false
		}
		exact[native]--
	}
	return true
}
