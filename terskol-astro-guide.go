package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	webview "github.com/webview/webview_go"
)

// =============================
// Embedded files and flags.
// =============================

//go:embed static/*
var embeddedFiles embed.FS

var portFlag = flag.Int("port", 8765, "HTTP port")

// =============================
// State model and commands.
// =============================

type applicationState struct {
	SocketPower string `json:"socket_power"`
}

type setPowerRequest struct {
	Power string `json:"power"`
}

type stateCommand struct {
	nextPower string
	reply     chan applicationState
}

// =============================
// Application bootstrap.
// =============================

func main() {
	// WebView on macOS is more stable when UI setup stays on the main locked thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stateCommands := make(chan stateCommand)
	go runStateOwner(ctx, stateCommands)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", handleGetState(stateCommands))
	mux.HandleFunc("/api/power", handleSetPower(stateCommands))
	mux.HandleFunc("/", serveEmbeddedStatic)

	address := fmt.Sprintf(":%d", *portFlag)
	httpServer := &http.Server{
		Addr:    address,
		Handler: withRequestLogging(mux),
	}

	log.Printf("startup: preparing HTTP server on %s", address)
	go runHTTPServer(httpServer)

	if err := waitHTTPServerReady("127.0.0.1", *portFlag, 5*time.Second); err != nil {
		log.Fatalf("startup: HTTP server is not ready: %v", err)
	}
	log.Printf("startup: HTTP server ready at http://127.0.0.1:%d", *portFlag)

	log.Printf("startup: creating WebView window")
	window := webview.New(false)
	if window == nil {
		log.Fatal("startup: webview init failed on this system")
	}
	defer window.Destroy()

	windowURL := fmt.Sprintf("http://127.0.0.1:%d", *portFlag)
	window.SetTitle("Minimal Socket Control")
	window.SetSize(900, 620, webview.HintNone)
	window.Navigate(windowURL)

	log.Printf("startup: WebView navigation started: %s", windowURL)
	window.Run()
	log.Printf("shutdown: WebView loop finished")

	runtime.KeepAlive(stateCommands)
}

func runHTTPServer(httpServer *http.Server) {
	log.Printf("startup: HTTP server listen begin on %s", httpServer.Addr)
	err := httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("shutdown: HTTP server stopped with error: %v", err)
		return
	}
	log.Printf("shutdown: HTTP server stopped")
}

// =============================
// State owner goroutine.
// =============================

func runStateOwner(ctx context.Context, stateCommands <-chan stateCommand) {
	currentState := applicationState{SocketPower: "off"}
	log.Printf("state: owner goroutine started with power=%s", currentState.SocketPower)

	for {
		select {
		case <-ctx.Done():
			log.Printf("state: owner goroutine stopped")
			return
		case command := <-stateCommands:
			if command.nextPower == "on" || command.nextPower == "off" {
				currentState.SocketPower = command.nextPower
				log.Printf("state: power updated to %s", currentState.SocketPower)
			}
			command.reply <- currentState
		}
	}
}

// =============================
// HTTP handlers.
// =============================

func handleGetState(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, _ *http.Request) {
		reply := make(chan applicationState, 1)
		stateCommands <- stateCommand{reply: reply}
		writeJSON(responseWriter, <-reply)
	}
}

func handleSetPower(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(responseWriter, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var apiRequest setPowerRequest
		if err := json.NewDecoder(request.Body).Decode(&apiRequest); err != nil {
			http.Error(responseWriter, "invalid json", http.StatusBadRequest)
			return
		}
		if apiRequest.Power != "on" && apiRequest.Power != "off" {
			http.Error(responseWriter, "power must be on or off", http.StatusBadRequest)
			return
		}

		reply := make(chan applicationState, 1)
		stateCommands <- stateCommand{nextPower: apiRequest.Power, reply: reply}
		writeJSON(responseWriter, <-reply)
	}
}

func writeJSON(responseWriter http.ResponseWriter, payload any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(responseWriter).Encode(payload); err != nil {
		http.Error(responseWriter, err.Error(), http.StatusInternalServerError)
	}
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		log.Printf("http: request started method=%s path=%s", request.Method, request.URL.Path)
		next.ServeHTTP(responseWriter, request)
		log.Printf("http: request finished method=%s path=%s duration=%s", request.Method, request.URL.Path, time.Since(startedAt))
	})
}

// =============================
// Static content.
// =============================

func serveEmbeddedStatic(responseWriter http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	file, err := embeddedFiles.ReadFile(filepath.Join("static", path))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(responseWriter, request)
		return
	}
	if err != nil {
		http.Error(responseWriter, "internal server error", http.StatusInternalServerError)
		return
	}

	responseWriter.Header().Set("Content-Type", contentType(path))
	_, _ = responseWriter.Write(file)
}

func contentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	default:
		return "application/octet-stream"
	}
}

func waitHTTPServerReady(host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	address := fmt.Sprintf("%s:%d", host, port)

	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 150*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for %s", address)
}
