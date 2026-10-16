// Package artifact validates and normalizes immutable Flyway SQL bundles.
package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrInvalid   = errors.New("invalid artifact bundle")
	ErrLimit     = errors.New("artifact limit exceeded")
	versionRE    = regexp.MustCompile(`^V([0-9]+(?:[._][0-9]+)*)__(.+)\.sql$`)
	repeatableRE = regexp.MustCompile(`^R__(.+)\.sql$`)
)

// Input is one uploaded file. Name may contain a folder-relative path.
type Input struct {
	Name  string
	Bytes []byte
}

// File describes a normalized SQL migration. Bytes are omitted from JSON manifests.
type File struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
	Bytes   []byte `json:"-"`
}

// Bundle retains the exact uploaded inputs and normalized SQL bytes.
type Bundle struct {
	Inputs     []Input `json:"-"`
	Files      []File  `json:"files"`
	SHA256     string  `json:"sha256"`
	TotalBytes int64   `json:"total_bytes"`
}

// Limits bound compressed input and expanded content. Zero fields use defaults.
type Limits struct {
	MaxUploadBytes    int64 `json:"max_upload_bytes"`
	MaxExpandedBytes  int64 `json:"max_expanded_bytes"`
	MaxFiles          int   `json:"max_files"`
	MaxFileBytes      int64 `json:"max_file_bytes"`
	MaxExpansionRatio int64 `json:"max_expansion_ratio"`
	MaxPathBytes      int   `json:"max_path_bytes"`
	MaxPathDepth      int   `json:"max_path_depth"`
}

func DefaultLimits() Limits {
	return Limits{MaxUploadBytes: 50 << 20, MaxExpandedBytes: 200 << 20, MaxFiles: 1000, MaxFileBytes: 20 << 20, MaxExpansionRatio: 200, MaxPathBytes: 1024, MaxPathDepth: 16}
}

func defaults(l Limits) Limits {
	d := DefaultLimits()
	if l.MaxUploadBytes <= 0 {
		l.MaxUploadBytes = d.MaxUploadBytes
	}
	if l.MaxExpandedBytes <= 0 {
		l.MaxExpandedBytes = d.MaxExpandedBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = d.MaxFileBytes
	}
	if l.MaxExpansionRatio <= 0 {
		l.MaxExpansionRatio = d.MaxExpansionRatio
	}
	if l.MaxPathBytes <= 0 {
		l.MaxPathBytes = d.MaxPathBytes
	}
	if l.MaxPathDepth <= 0 {
		l.MaxPathDepth = d.MaxPathDepth
	}
	return l
}

// Normalize recognizes SQL inputs and ZIP, TAR, TAR.GZ or TGZ archives by structure.
// All returned bytes are copies and all paths use slash separators.
func Normalize(inputs []Input, limits Limits) (Bundle, error) {
	l := defaults(limits)
	if len(inputs) > l.MaxFiles {
		return Bundle{}, ErrLimit
	}
	if len(inputs) == 0 {
		return Bundle{}, fmt.Errorf("%w: no input files", ErrInvalid)
	}
	b := Bundle{Inputs: make([]Input, 0, len(inputs))}
	seenInputs := map[string]bool{}
	var inputSize int64
	for _, in := range inputs {
		if len(in.Bytes) == 0 {
			return Bundle{}, fmt.Errorf("%w: empty input", ErrInvalid)
		}
		if int64(len(in.Bytes)) > l.MaxUploadBytes-inputSize {
			return Bundle{}, ErrLimit
		}
		inputSize += int64(len(in.Bytes))
		clean, err := safePathWithLimits(in.Name, l)
		if err != nil {
			return Bundle{}, err
		}
		if seenInputs[clean] {
			return Bundle{}, fmt.Errorf("%w: duplicate path %q", ErrInvalid, clean)
		}
		seenInputs[clean] = true
		copyIn := Input{Name: clean, Bytes: bytes.Clone(in.Bytes)}
		b.Inputs = append(b.Inputs, copyIn)
	}
	// Deterministic handling independent of caller selection order.
	sort.Slice(b.Inputs, func(i, j int) bool { return b.Inputs[i].Name < b.Inputs[j].Name })
	for _, in := range b.Inputs {
		var entries []Input
		var err error
		switch {
		case isZIP(in.Bytes):
			entries, err = readZIP(in.Bytes, l)
		case isGZIP(in.Bytes):
			entries, err = readTGZ(in.Bytes, l)
		case isTAR(in.Bytes) || strings.EqualFold(path.Ext(in.Name), ".tar"):
			entries, err = readTAR(in.Bytes, l)
		default:
			if !strings.EqualFold(path.Ext(in.Name), ".sql") {
				return Bundle{}, fmt.Errorf("%w: unsupported input %q", ErrInvalid, in.Name)
			}
			entries = []Input{in}
		}
		if err != nil {
			return Bundle{}, err
		}
		for _, e := range entries {
			p, err := safePathWithLimits(e.Name, l)
			if err != nil {
				return Bundle{}, err
			}
			if !strings.EqualFold(path.Ext(p), ".sql") {
				return Bundle{}, fmt.Errorf("%w: non-SQL entry %q", ErrInvalid, p)
			}
			if isArchiveName(p) || isArchiveBytes(e.Bytes) {
				return Bundle{}, fmt.Errorf("%w: nested archive %q", ErrInvalid, p)
			}
			if int64(len(e.Bytes)) > l.MaxFileBytes {
				return Bundle{}, ErrLimit
			}
			if int64(len(e.Bytes)) > l.MaxExpandedBytes-b.TotalBytes {
				return Bundle{}, ErrLimit
			}
			b.TotalBytes += int64(len(e.Bytes))
			b.Files = append(b.Files, File{Path: p, Bytes: bytes.Clone(e.Bytes)})
		}
	}
	if len(b.Files) == 0 || len(b.Files) > l.MaxFiles {
		return Bundle{}, ErrLimit
	}
	sort.Slice(b.Files, func(i, j int) bool { return b.Files[i].Path < b.Files[j].Path })
	seen := map[string]bool{}
	versions := map[string]string{}
	repeatables := map[string]string{}
	for i := range b.Files {
		f := &b.Files[i]
		if seen[f.Path] {
			return Bundle{}, fmt.Errorf("%w: duplicate migration path %q", ErrInvalid, f.Path)
		}
		seen[f.Path] = true
		base := path.Base(f.Path)
		if m := versionRE.FindStringSubmatch(base); m != nil {
			v, err := canonicalVersion(m[1])
			if err != nil {
				return Bundle{}, fmt.Errorf("%w: invalid version in %q", ErrInvalid, base)
			}
			if prev, ok := versions[v]; ok {
				return Bundle{}, fmt.Errorf("%w: version collision %q and %q", ErrInvalid, prev, base)
			}
			versions[v] = base
			f.Kind = "versioned"
			f.Version = v
		} else if repeatableRE.MatchString(base) {
			description := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(base, "R__"), ".sql"))
			if prev, ok := repeatables[description]; ok {
				return Bundle{}, fmt.Errorf("%w: repeatable collision %q and %q", ErrInvalid, prev, base)
			}
			repeatables[description] = base
			f.Kind = "repeatable"
		} else {
			return Bundle{}, fmt.Errorf("%w: unsupported Flyway migration filename %q", ErrInvalid, base)
		}
		s := sha256.Sum256(f.Bytes)
		f.SHA256 = hex.EncodeToString(s[:])
	}
	// Manifest digest covers canonical path + content digest pairs.
	var manifest strings.Builder
	for _, f := range b.Files {
		manifest.WriteString(f.Path)
		manifest.WriteByte(0)
		manifest.WriteString(f.SHA256)
		manifest.WriteByte('\n')
	}
	s := sha256.Sum256([]byte(manifest.String()))
	b.SHA256 = hex.EncodeToString(s[:])
	return b, nil
}

func safePathWithLimits(name string, l Limits) (string, error) {
	if name == "" || len(name) > l.MaxPathBytes || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w: unsafe path %q", ErrInvalid, name)
	}
	parts := strings.Split(name, "/")
	if len(parts) > l.MaxPathDepth {
		return "", fmt.Errorf("%w: path nesting limit", ErrLimit)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: unsafe path", ErrInvalid)
		}
	}
	for _, part := range parts {
		if part == ".." || part == "." || part == "" {
			return "", fmt.Errorf("%w: unsafe path %q", ErrInvalid, name)
		}
	}
	clean := path.Clean(name)
	if clean != name || path.IsAbs(clean) {
		return "", fmt.Errorf("%w: unsafe path %q", ErrInvalid, name)
	}
	return clean, nil
}
func isArchiveName(n string) bool {
	e := strings.ToLower(path.Ext(n))
	return e == ".zip" || e == ".tar" || e == ".gz" || e == ".tgz" || e == ".bz2" || e == ".xz"
}
func isZIP(b []byte) bool          { return len(b) >= 4 && bytes.Equal(b[:4], []byte{'P', 'K', 3, 4}) }
func isGZIP(b []byte) bool         { return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b }
func isTAR(b []byte) bool          { return len(b) >= 262 && string(b[257:262]) == "ustar" }
func isArchiveBytes(b []byte) bool { return isZIP(b) || isGZIP(b) || isTAR(b) }

func readZIP(data []byte, l Limits) ([]Input, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: malformed ZIP", ErrInvalid)
	}
	var out []Input
	var total int64
	if len(r.File) > l.MaxFiles {
		return nil, ErrLimit
	}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			name := strings.TrimSuffix(f.Name, "/")
			if _, err := safePathWithLimits(name, l); err != nil {
				return nil, err
			}
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: ZIP links or special entries unsupported", ErrInvalid)
		}
		name, err := safePathWithLimits(f.Name, l)
		if err != nil {
			return nil, err
		}
		if f.UncompressedSize64 > uint64(l.MaxFileBytes) {
			return nil, ErrLimit
		}
		total += int64(f.UncompressedSize64)
		if total > l.MaxExpandedBytes {
			return nil, ErrLimit
		}
		if f.CompressedSize64 > uint64(len(data)) {
			return nil, fmt.Errorf("%w: malformed ZIP size", ErrInvalid)
		}
		if f.UncompressedSize64 > 0 && f.UncompressedSize64 > f.CompressedSize64*uint64(l.MaxExpansionRatio)+1024 {
			return nil, ErrLimit
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: ZIP entry", ErrInvalid)
		}
		data, err := io.ReadAll(io.LimitReader(rc, l.MaxFileBytes+1))
		closeErr := rc.Close()
		if err != nil || closeErr != nil {
			return nil, fmt.Errorf("%w: ZIP entry read", ErrInvalid)
		}
		if int64(len(data)) > l.MaxFileBytes {
			return nil, ErrLimit
		}
		out = append(out, Input{Name: name, Bytes: data})
		if len(out) > l.MaxFiles {
			return nil, ErrLimit
		}
	}
	return out, nil
}
func readTAR(data []byte, l Limits) ([]Input, error) { return readTarReader(bytes.NewReader(data), l) }
func readTarReader(r io.Reader, l Limits) ([]Input, error) {
	tr := tar.NewReader(r)
	var out []Input
	var total int64
	entries := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: malformed TAR", ErrInvalid)
		}
		entries++
		if entries > l.MaxFiles {
			return nil, ErrLimit
		}
		if h.Typeflag == tar.TypeDir {
			name := strings.TrimSuffix(h.Name, "/")
			if _, err := safePathWithLimits(name, l); err != nil {
				return nil, err
			}
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("%w: TAR links or special entries unsupported", ErrInvalid)
		}
		name, err := safePathWithLimits(h.Name, l)
		if err != nil {
			return nil, err
		}
		if h.Size < 0 || h.Size > l.MaxFileBytes {
			return nil, ErrLimit
		}
		total += h.Size
		if total > l.MaxExpandedBytes {
			return nil, ErrLimit
		}
		data, err := io.ReadAll(io.LimitReader(tr, l.MaxFileBytes+1))
		if err != nil {
			return nil, fmt.Errorf("%w: TAR entry read", ErrInvalid)
		}
		if int64(len(data)) != h.Size {
			return nil, fmt.Errorf("%w: truncated TAR entry", ErrInvalid)
		}
		out = append(out, Input{Name: name, Bytes: data})
		if len(out) > l.MaxFiles {
			return nil, ErrLimit
		}
	}
	return out, nil
}
func readTGZ(data []byte, l Limits) ([]Input, error) {
	compressed := bytes.NewReader(data)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed gzip", ErrInvalid)
	}
	gz.Multistream(false)
	archive, err := io.ReadAll(io.LimitReader(gz, l.MaxExpandedBytes+1))
	closeErr := gz.Close()
	if err != nil || closeErr != nil {
		return nil, fmt.Errorf("%w: invalid or oversized gzip stream", ErrInvalid)
	}
	if compressed.Len() != 0 {
		return nil, fmt.Errorf("%w: trailing gzip data or additional streams", ErrInvalid)
	}
	if int64(len(archive)) > l.MaxExpandedBytes {
		return nil, ErrLimit
	}
	if len(archive) > 0 && int64(len(archive)) > int64(len(data))*l.MaxExpansionRatio+1024 {
		return nil, ErrLimit
	}
	return readTarReader(bytes.NewReader(archive), l)
}
func canonicalVersion(v string) (string, error) {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '_' })
	if len(parts) == 0 {
		return "", ErrInvalid
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return "", err
		}
		out[i] = strconv.FormatUint(n, 10)
	}
	// Flyway's numeric version ordering treats trailing zero components as equivalent.
	for len(out) > 1 && out[len(out)-1] == "0" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "."), nil
}

// EffectiveLimits fills omitted fields with bounded application defaults.
func EffectiveLimits(l Limits) Limits { return defaults(l) }
