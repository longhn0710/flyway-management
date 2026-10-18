package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"flyway-manager/internal/core"
)

func TestExternalCollectionCursorBindingAndPaginationKinds(t *testing.T) {
	actor := core.Actor{Kind: "service", ID: "svc-1"}
	request := func(path, cursor string) *http.Request {
		t.Helper()
		target := path
		if cursor != "" {
			target += "?cursor=" + cursor
		}
		return httptest.NewRequest("GET", target, nil)
	}

	collectionPath := "/api/v2/projects"
	cursor := encodeExternalAfterCursor(collectionPath, actor, "project-50")
	r := request(collectionPath, cursor)
	limit, after, err := externalCollectionPage(r, actor)
	if err != nil || limit != 50 || after != "project-50" {
		t.Fatalf("valid collection cursor: limit=%d after=%q err=%v", limit, after, err)
	}

	for name, tc := range map[string]struct {
		path   string
		actor  core.Actor
		cursor string
	}{
		"different resource": {path: "/api/v2/projects/p/deployment-targets", actor: actor, cursor: cursor},
		"different actor":    {path: collectionPath, actor: core.Actor{Kind: "service", ID: "svc-2"}, cursor: cursor},
		"tampered cursor":    {path: collectionPath, actor: actor, cursor: cursor + "x"},
	} {
		t.Run(name, func(t *testing.T) {
			r := request(tc.path, tc.cursor)
			if _, _, err := externalCollectionPage(r, tc.actor); err == nil {
				t.Fatal("invalid cursor was accepted")
			}
		})
	}

	logPath := "/api/v2/projects/p/migration-runs/r/logs"
	logCursor := encodeExternalCursor(logPath, actor, 25)
	r = request(logPath, logCursor)
	_, offset, err := externalPage(r, actor)
	if err != nil || offset != 25 {
		t.Fatalf("valid log cursor: offset=%d err=%v", offset, err)
	}
	r = request(logPath, cursor)
	if _, _, err = externalPage(r, actor); err == nil {
		t.Fatal("collection cursor was accepted as a log offset")
	}
	r = request(collectionPath, logCursor)
	if _, _, err = externalCollectionPage(r, actor); err == nil {
		t.Fatal("log offset cursor was accepted as a collection key")
	}
}
