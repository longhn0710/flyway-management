//go:build !linux && !darwin && !windows

package flyway

import "errors"

func renameNoReplace(string, string) error {
	return errors.New("atomic no-replace publication is unsupported on this platform")
}
