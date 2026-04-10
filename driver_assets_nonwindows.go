//go:build !windows

package main

func prepareWindowsDriverDirectory() (func(), error) {
	return func() {}, nil
}
