//go:build windows || !cgo

package main

import (
	"log"
	"os"
	"os/signal"
)

func runUserInterface(address string) {
	log.Printf("ui: webview is not available for this build target")
	log.Printf("ui: open http://localhost%s in your browser", address)
	waitForInterrupt()
}

func waitForInterrupt() {
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt)
	<-signalChannel
	log.Printf("shutdown: interrupt signal received")
}
