//go:build !cgo

package main

import "log"

func runUserInterface(address string) {
	log.Fatalf("ui: webview requires CGO; rebuild with CGO_ENABLED=1 and a C compiler toolchain, then open http://localhost%s", address)
}
