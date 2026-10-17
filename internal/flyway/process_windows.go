//go:build windows

package flyway

import (
	"errors"
	"os/exec"
)

func configureProcessGroup(cmd *exec.Cmd) {}

func processGroupID(pid int) (int, error) {
	return 0, errors.New("PostgreSQL execution requires the qualified Linux process-group profile")
}
