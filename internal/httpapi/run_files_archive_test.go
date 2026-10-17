package httpapi

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"flyway-manager/internal/artifact"
	"flyway-manager/internal/core"
)

func makeZIP(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range entries {
		part, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRunFilesArchiveUploadAndBoundContent(t *testing.T) {
	f, runner := executionFixture(t, artifact.DefaultLimits())
	// The fixture reports the versioned migration as already applied so the
	// worker can exercise archive dispatch without simulating a live database.
	runner.applied = true
	resources := createExecutionResources(t, f)
	versioned := []byte("-- exact versioned bytes\r\nSELECT 1 FROM dual;\r\n")
	repeatable := []byte("-- exact repeatable bytes\r\nSELECT 2 FROM dual;\r\n")
	originalZIP := makeZIP(t, map[string][]byte{
		"nested/V1__archive.sql": versioned,
		"repeat/R__archive.sql":  repeatable,
	})
	uploaded := decodeData[core.Artifact](t, multipartRequest(t, f, resources.target.ID, resources.schema.ID, map[string][]byte{"release.zip": originalZIP}, f.cookie, nil), 201)
	if len(uploaded.Files) != 2 || len(uploaded.Inputs) != 1 || uploaded.Inputs[0].Size != len(originalZIP) {
		t.Fatalf("unexpected normalized archive manifest: files=%+v inputs=%+v", uploaded.Files, uploaded.Inputs)
	}

	input := core.RunInput{TargetID: resources.target.ID, SchemaID: resources.schema.ID, ExecutionAccountID: resources.account.ID, ArtifactID: uploaded.ID}
	run := decodeData[core.Run](t, f.call("POST", "/migration-runs", input, f.cookie, map[string]string{"Idempotency-Key": "archive-run-files"}), 202)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current := decodeData[core.Run](t, f.call("GET", "/migration-runs/"+run.ID, nil, f.cookie, nil), 200)
		run = current
		if current.Status == "SUCCEEDED" || current.Status == "BLOCKED" || current.Status == "FAILED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Status != "SUCCEEDED" {
		logs := f.call("GET", "/migration-runs/"+run.ID+"/logs?limit=100", nil, f.cookie, nil)
		t.Fatalf("archive run did not succeed: %s logs=%s", run.Status, logs.Body.String())
	}

	files := decodeData[[]core.RunFile](t, f.call("GET", "/migration-runs/"+run.ID+"/files", nil, f.cookie, nil), 200)
	if len(files) != 2 {
		t.Fatalf("effective archive file count=%d, want 2: %+v", len(files), files)
	}
	expected := map[string][]byte{"nested/V1__archive.sql": versioned, "repeat/R__archive.sql": repeatable}
	for _, file := range files {
		content, ok := expected[file.Path]
		if !ok {
			t.Fatalf("unexpected effective path %q", file.Path)
		}
		if file.ArtifactID != uploaded.ID || file.Size != len(content) {
			t.Fatalf("wrong run item metadata: %+v", file)
		}
		sum := sha256.Sum256(content)
		if file.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("wrong checksum for %s: %s", file.Path, file.SHA256)
		}
		wantKind, wantVersion := "repeatable", ""
		if strings.HasPrefix(file.Path, "nested/V") {
			wantKind, wantVersion = "versioned", "1"
		}
		if file.Kind != wantKind || file.Version != wantVersion {
			t.Fatalf("wrong migration classification for %s: kind=%s version=%s", file.Path, file.Kind, file.Version)
		}
		path := url.QueryEscape(file.Path)
		got := f.call("GET", fmt.Sprintf("/migration-runs/%s/files/content?artifact_id=%s&path=%s", run.ID, uploaded.ID, path), nil, f.cookie, nil)
		assertStatus(t, got, 200)
		if !bytes.Equal(got.Body.Bytes(), content) {
			t.Fatalf("run-bound bytes changed for %s", file.Path)
		}
	}
	original := f.call("GET", "/artifacts/"+uploaded.ID+"/inputs?index=0", nil, f.cookie, nil)
	assertStatus(t, original, 200)
	if !bytes.Equal(original.Body.Bytes(), originalZIP) {
		t.Fatal("original submitted ZIP bytes changed")
	}

	unsupported := makeZIP(t, map[string][]byte{"README.md": []byte("not a migration")})
	assertStatus(t, multipartRequest(t, f, resources.target.ID, resources.schema.ID, map[string][]byte{"invalid.zip": unsupported}, f.cookie, nil), 400)
}
