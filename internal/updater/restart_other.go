//go:build !linux

package updater

import "errors"

// restartSelf is a no-op stub for non-Linux builds. Apply already refuses on
// non-Linux platforms, so this only exists to keep the package compiling on the
// developer's machine.
func restartSelf(exePath string) error {
	return errors.New("自更新仅支持 Linux")
}
