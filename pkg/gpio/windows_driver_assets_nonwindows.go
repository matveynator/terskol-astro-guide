//go:build !windows

package gpio

import "io/fs"

func PrepareWindowsDriverDirectory(embeddedFiles fs.FS) (func(), error) {
	_ = embeddedFiles
	return func() {}, nil
}
