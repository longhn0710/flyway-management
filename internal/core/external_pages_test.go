package core

import (
	"fmt"
	"testing"
)

func TestExternalPagesUseStableContinuationAndCurrentAuthorization(t *testing.T) {
	f, _, d := externalFixture(t)
	for i := range 4 {
		_, e := f.s.SaveProject(testCtx, f.trace(), "", ProjectInput{Name: fmt.Sprintf("page-%d", i), Status: "active"})
		must(t, e)
	}
	seen := map[string]bool{}
	after := ""
	for range 8 {
		items, next, e := f.s.ExternalProjectsPage(testCtx, f.admin, after, 2)
		must(t, e)
		if len(items) > 2 {
			t.Fatal("page limit ignored")
		}
		for _, p := range items {
			if seen[p.ID] || p.ID <= after {
				t.Fatal("duplicate/out-of-order project")
			}
			seen[p.ID] = true
		}
		if next == "" {
			break
		}
		after = next
	}
	if len(seen) != 5 {
		t.Fatalf("projects=%d", len(seen))
	}
	for i := range 3 {
		in := d.DeploymentTargetInput
		in.Name = fmt.Sprintf("destination-%d", i)
		_, e := f.s.SaveDeploymentTarget(testCtx, f.trace(), d.ProjectID, "", in)
		must(t, e)
	}
	a, _ := externalService(t, f, d, []TokenDeploymentScope{{DeploymentTargetID: d.ID, Read: true}})
	visible, next, e := f.s.DeploymentTargetsPage(testCtx, a, d.ProjectID, "", 1)
	must(t, e)
	if len(visible) != 1 || visible[0].ID != d.ID {
		t.Fatal("target isolation")
	}
	// A keyset continuation must not repeat earlier resources after a grant changes.
	visible, _, e = f.s.DeploymentTargetsPage(testCtx, a, d.ProjectID, next, 1)
	must(t, e)
	if len(visible) != 0 {
		t.Fatal("scope leaked on continuation")
	}
	_, _, e = f.s.ExternalProjectsPage(testCtx, f.admin, "", 201)
	status(t, e, 400)
}
func TestVisibleExternalPageScansBoundedBatchesAndFiltersBeforeLimit(t *testing.T) {
	calls := 0
	fetch := func(after string) ([]string, error) {
		calls++
		out := []string{}
		for i := 0; i < 401; i++ {
			id := fmt.Sprintf("%04d", i)
			if id > after && len(out) < 200 {
				out = append(out, id)
			}
		}
		return out, nil
	}
	view := func(id string) (string, error) {
		if id < "0200" {
			return "", missing()
		}
		return id, nil
	}
	items, next, e := visibleExternalPage("", 2, fetch, view)
	must(t, e)
	if calls != 2 || len(items) != 2 || items[0] != "0200" || next != "0201" {
		t.Fatalf("calls=%d items=%v cursor=%s", calls, items, next)
	}
	items, _, e = visibleExternalPage(next, 2, fetch, view)
	must(t, e)
	if items[0] != "0202" {
		t.Fatal("continuation repeated rows")
	}
}
