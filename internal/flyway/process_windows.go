//go:build windows

package flyway

import "os/exec"

func configureProcessGroup(cmd *exec.Cmd) {}
