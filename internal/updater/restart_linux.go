//go:build linux

package updater

import (
	"os"
	"syscall"
)

// restartSelf replaces the current process image with the freshly-installed
// binary at exePath, preserving args and environment. On success it does not
// return; the same PID now runs the new code, so systemd sees no exit.
func restartSelf(exePath string) error {
	argv := append([]string{exePath}, os.Args[1:]...)
	return syscall.Exec(exePath, argv, os.Environ())
}
