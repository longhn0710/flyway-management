package flyway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	maxDistributionBytes = int64(2 << 30)
	maxDistributionFiles = 50000
	maxManifestBytes     = int64(16 << 20)
)

var installMu sync.Mutex
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[-+][A-Za-z0-9.-]+)?$`)

// Install copies an operator-provided, checksum-pinned Flyway distribution into
// dataDir/runtime/flyway. It never downloads files or replaces an installation.
func Install(ctx context.Context, sourceDir, executableRelativePath, version, manifestSHA256, dataDir string) (Config, error) {
	installMu.Lock()
	defer installMu.Unlock()

	if sourceDir == "" || dataDir == "" {
		return Config{}, errors.New("source and data directories are required")
	}
	if !versionPattern.MatchString(version) || !isSHA256(manifestSHA256) {
		return Config{}, errors.New("invalid Flyway version or distribution pin")
	}
	rel, err := safeInstallPath(executableRelativePath)
	if err != nil {
		return Config{}, err
	}
	source, err := filepath.Abs(sourceDir)
	if err != nil {
		return Config{}, errors.New("invalid source distribution directory")
	}
	rootInfo, err := os.Lstat(source)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return Config{}, errors.New("source distribution must be a real directory")
	}
	sourceExe := filepath.Join(source, rel)
	candidate := Config{Executable: sourceExe, Version: version, SHA256: manifestSHA256, TimeoutSeconds: 300}
	sourceRuntime := New(candidate, dataDir)
	if err := sourceRuntime.verifyManifest(source); err != nil {
		return Config{}, fmt.Errorf("source distribution integrity failed: %w", err)
	}
	if err := verifyOracleJars(source); err != nil {
		return Config{}, fmt.Errorf("source distribution dependency check failed: %w", err)
	}
	exeInfo, err := os.Lstat(sourceExe)
	if err != nil || !exeInfo.Mode().IsRegular() || exeInfo.Mode()&0111 == 0 {
		return Config{}, errors.New("configured source executable is not a regular executable file")
	}

	installRoot := filepath.Join(dataDir, "runtime", "flyway")
	if err := secureDirectory(installRoot); err != nil {
		return Config{}, errors.New("unable to prepare private Flyway installation directory")
	}
	pin := strings.ToLower(manifestSHA256)
	target := filepath.Join(installRoot, version+"-"+pin[:12])
	if _, err := os.Lstat(target); err == nil {
		return Config{}, errors.New("pinned Flyway installation already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, errors.New("unable to inspect pinned Flyway installation path")
	}
	stage, err := os.MkdirTemp(installRoot, ".flyway-stage-")
	if err != nil {
		return Config{}, errors.New("unable to create Flyway staging directory")
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0700); err != nil {
		return Config{}, errors.New("unable to secure Flyway staging directory")
	}
	if err := copyDistribution(ctx, source, stage); err != nil {
		return Config{}, fmt.Errorf("unable to copy Flyway distribution: %w", err)
	}
	stageExe := filepath.Join(stage, rel)
	stagedConfig := Config{Executable: stageExe, Version: version, SHA256: manifestSHA256, TimeoutSeconds: 300}
	stagedRuntime := New(stagedConfig, dataDir)
	readiness := stagedRuntime.Check(ctx)
	if !readiness.Ready {
		return Config{}, fmt.Errorf("staged Flyway distribution is not ready: %s", strings.Join(readiness.Blockers, "; "))
	}
	if err := syncDirectory(stage); err != nil {
		return Config{}, errors.New("unable to sync Flyway staging directory")
	}
	// installRoot is private and all in-process installers are serialized. The
	// final name is immutable; a pre-existing destination is never overwritten.
	if _, err := os.Lstat(target); err == nil {
		return Config{}, errors.New("pinned Flyway installation already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, errors.New("unable to inspect pinned Flyway installation path")
	}
	if err := renameNoReplace(stage, target); err != nil {
		return Config{}, errors.New("unable to publish Flyway installation")
	}
	published := true
	defer func() {
		if published {
			_ = os.RemoveAll(target)
		}
	}()
	if err := syncDirectory(installRoot); err != nil {
		return Config{}, errors.New("unable to sync Flyway installation directory")
	}
	config := Config{Executable: filepath.Join(target, rel), Version: version, SHA256: manifestSHA256, TimeoutSeconds: 300}
	readiness = New(config, dataDir).Check(ctx)
	if !readiness.Ready {
		return Config{}, fmt.Errorf("installed Flyway distribution failed final verification: %s", strings.Join(readiness.Blockers, "; "))
	}
	published = false
	return config, nil
}

func safeInstallPath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return "", errors.New("invalid relative Flyway executable path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.ToSlash(clean) != name {
		return "", errors.New("invalid relative Flyway executable path")
	}
	return clean, nil
}

func secureDirectory(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Create each managed path component and reject symlinks so private content
	// cannot be redirected outside the selected data directory.
	parent := filepath.Dir(abs)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	if err := rejectSymlinkPath(parent); err != nil {
		return err
	}
	if err := os.Mkdir(abs, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed path is not a real directory")
	}
	if err := os.Chmod(abs, 0700); err != nil {
		return err
	}
	return nil
}

func rejectSymlinkPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(abs, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("path contains a symlink or non-directory")
		}
	}
	return nil
}

func copyDistribution(ctx context.Context, source, destination string) error {
	var total int64
	count := 0
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source contains symlink")
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return errors.New("source contains non-regular file")
		}
		count++
		if count > maxDistributionFiles {
			return errors.New("distribution has too many files")
		}
		if info.Size() < 0 || total > maxDistributionBytes-info.Size() {
			return errors.New("distribution exceeds size limit")
		}
		total += info.Size()
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		openedInfo, statErr := in.Stat()
		if statErr != nil || !os.SameFile(info, openedInfo) {
			_ = in.Close()
			return errors.New("source file changed during copy")
		}
		mode := os.FileMode(0600)
		if info.Mode().Perm()&0111 != 0 {
			mode |= 0100
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			_ = in.Close()
			return err
		}
		written, copyErr := io.Copy(out, io.LimitReader(contextReader{ctx: ctx, reader: in}, info.Size()+1))
		closeInErr := in.Close()
		syncErr := out.Sync()
		closeOutErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInErr != nil {
			return closeInErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		if written != info.Size() {
			return errors.New("source file changed during copy")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return syncTreeDirectories(destination)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func syncTreeDirectories(root string) error {
	var dirs []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncDirectory(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
