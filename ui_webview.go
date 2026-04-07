//go:build (linux || darwin || windows) && cgo

package main

import (
	"log"

	"github.com/webview/webview"
)

func runUserInterface(address string) {
	window := webview.New(false)
	defer window.Destroy()
	window.SetTitle("DIO/DO Control · ECX-1000-2G")
	window.SetSize(980, 760, webview.HintNone)
	window.Navigate("http://localhost" + address)

	log.Printf("webview: window started")
	window.Run()
	log.Printf("shutdown: webview stopped")
}
