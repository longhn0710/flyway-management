package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, v := range files {
		f, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(v); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func tarBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for n, v := range files {
		if e := w.WriteHeader(&tar.Header{Name: n, Mode: 0600, Size: int64(len(v)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			t.Fatal(e)
		}
		if _, e := w.Write(v); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func tgzBytes(t *testing.T, tarData []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, e := w.Write(tarData); e != nil {
		t.Fatal(e)
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestNormalizeFormatsAndPreservesBytes(t *testing.T) {
	payload := []byte("-- exact bytes\r\nSELECT 1;\r\n")
	for name, data := range map[string][]byte{"V1__init.sql": payload, "bundle.zip": zipBytes(t, map[string][]byte{"nested/V1__init.sql": payload}), "bundle.tar": tarBytes(t, map[string][]byte{"nested/V1__init.sql": payload}), "bundle.tgz": tgzBytes(t, tarBytes(t, map[string][]byte{"nested/V1__init.sql": payload}))} {
		b, e := Normalize([]Input{{Name: name, Bytes: data}}, Limits{})
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		if len(b.Files) != 1 || !bytes.Equal(b.Files[0].Bytes, payload) {
			t.Fatalf("%s bytes differ", name)
		}
		if b.Files[0].Kind != "versioned" || b.Files[0].Version != "1" || b.Files[0].SHA256 == "" || b.SHA256 == "" {
			t.Fatalf("bad manifest: %+v", b.Files[0])
		}
	}
}
func TestNormalizeFolderAndRepeatable(t *testing.T) {
	b, e := Normalize([]Input{{"dir/R__refresh.sql", []byte("SELECT 2;")}, {"dir/V2_1__next.sql", []byte("SELECT 1;")}}, Limits{})
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Files) != 2 || b.Files[0].Path != "dir/R__refresh.sql" || b.Files[0].Kind != "repeatable" || b.Files[1].Version != "2.1" {
		t.Fatalf("unexpected files: %+v", b.Files)
	}
	encoded, e := json.Marshal(b)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(encoded, []byte("SELECT")) {
		t.Fatal("manifest exposed SQL bytes")
	}
}
func TestRejectsCanonicalVersionCollisions(t *testing.T) {
	_, e := Normalize([]Input{{"V1.02__a.sql", []byte("x")}, {"V1_2__b.sql", []byte("y")}}, Limits{})
	if !errors.Is(e, ErrInvalid) {
		t.Fatalf("want invalid collision, got %v", e)
	}
}
func TestRejectsUnsafeAndUnsupportedEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{{"x.zip", zipBytes(t, map[string][]byte{"../V1__a.sql": []byte("x")})}, {"x.zip", zipBytes(t, map[string][]byte{"V1__a.sql": []byte("x"), "evil.txt": []byte("x")})}, {"x.sql", []byte("x")}, {"../V1__a.sql", []byte("x")}} {
		if _, e := Normalize([]Input{{tc.name, tc.data}}, Limits{}); e == nil {
			t.Fatalf("accepted unsafe input %q", tc.name)
		}
	}
}
func TestRejectsArchiveLimits(t *testing.T) {
	data := zipBytes(t, map[string][]byte{"V1__a.sql": bytes.Repeat([]byte("a"), 8192)})
	_, e := Normalize([]Input{{"x.zip", data}}, Limits{MaxUploadBytes: 1 << 20, MaxExpandedBytes: 1 << 20, MaxFiles: 10, MaxFileBytes: 1024, MaxExpansionRatio: 1000})
	if !errors.Is(e, ErrLimit) {
		t.Fatalf("want limit error, got %v", e)
	}
	_, e = Normalize([]Input{{"V1__a.sql", []byte("xx")}}, Limits{MaxUploadBytes: 1})
	if !errors.Is(e, ErrLimit) {
		t.Fatalf("want upload limit, got %v", e)
	}
}

func TestRejectsRepeatableCollisionsAndExcessivePathDepth(t *testing.T) {
	_, err := Normalize([]Input{{"a/R__refresh.sql", []byte("a")}, {"b/R__REFRESH.sql", []byte("b")}}, Limits{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want repeatable collision, got %v", err)
	}
	deep := strings.Repeat("d/", DefaultLimits().MaxPathDepth) + "V1__init.sql"
	_, err = Normalize([]Input{{deep, []byte("x")}}, Limits{})
	if err == nil {
		t.Fatal("accepted path deeper than configured limit")
	}
}

func TestRejectsGzipCorruptionAndTrailingStream(t *testing.T) {
	valid := tgzBytes(t, tarBytes(t, map[string][]byte{"V1__init.sql": []byte("x")}))
	corrupt := bytes.Clone(valid)
	corrupt[len(corrupt)-1] ^= 0xff
	if _, err := Normalize([]Input{{"broken.tgz", corrupt}}, Limits{}); err == nil {
		t.Fatal("accepted invalid gzip checksum")
	}
	joined := append(bytes.Clone(valid), valid...)
	if _, err := Normalize([]Input{{"joined.tgz", joined}}, Limits{}); err == nil {
		t.Fatal("accepted concatenated gzip streams")
	}
}

func TestRejectsZIPSymlink(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "V1__link.sql"}
	h.SetMode(os.ModeSymlink | 0o777)
	f, err := w.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("target"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize([]Input{{"links.zip", b.Bytes()}}, Limits{}); err == nil {
		t.Fatal("accepted ZIP symlink")
	}
}
