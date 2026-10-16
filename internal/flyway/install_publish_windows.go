//go:build windows

package flyway

import "os"

// Windows MoveFileW, used by os.Rename, fails when the destination exists.
func renameNoReplace(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
