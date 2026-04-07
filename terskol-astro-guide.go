package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/webview/webview"
)

// =============================
// Embedded static assets.
// =============================

//go:embed static/*
var staticFiles embed.FS

var (
	portFlag         = flag.Int("port", 8765, "web server port")
	directoryFlag    = flag.String("directory", ".", "directory to serve files from")
	dioValueFileFlag = flag.String("dio-value-file", "/sys/class/gpio/gpio0/value", "first DIO port value file")
)

// =============================
// DIO command pipeline.
// =============================

type dioCommand struct {
	nextPower string
	reply     chan dioReply
}

type dioReply struct {
	power string
	err   error
}

// =============================
// Main entry point.
// =============================

func main() {
	flag.Parse()

	dioCommands := make(chan dioCommand)
	go runFirstDIOOwner(dioCommands, *dioValueFileFlag)

	http.HandleFunc("/api/state", handleDIOState(dioCommands))
	http.HandleFunc("/api/power", handleDIOPower(dioCommands))
	http.HandleFunc("/", handleRequest)

	address := fmt.Sprintf(":%d", *portFlag)
	log.Printf("startup: starting HTTP server on http://localhost%s", address)
	go func() {
		err := http.ListenAndServe(address, nil)
		if err != nil {
			log.Printf("shutdown: HTTP server stopped: %v", err)
		}
	}()

	onPageLoaded := func(_ webview.WebView, loadedURL string) {
		log.Printf("webview: loaded %s", loadedURL)
	}

	window := webview.New(false)
	defer window.Destroy()

	window.SetTitle("DIO Control · ECX-1000-2G")
	window.SetSize(800, 600, webview.HintNone)
	window.Navigate("http://localhost" + address)
	_ = window.Bind("onPageLoaded", onPageLoaded)

	log.Printf("webview: window started")
	window.Run()
	log.Printf("shutdown: webview stopped")
}

func runFirstDIOOwner(dioCommands <-chan dioCommand, dioValueFile string) {
	currentPower := "off"
	log.Printf("dio: owner started, target=%s", dioValueFile)

	for command := range dioCommands {
		if command.nextPower == "" {
			command.reply <- dioReply{power: currentPower}
			continue
		}

		if command.nextPower != "on" && command.nextPower != "off" {
			command.reply <- dioReply{power: currentPower, err: errors.New("power must be on or off")}
			continue
		}

		if err := writeFirstDIOPower(dioValueFile, command.nextPower); err != nil {
			command.reply <- dioReply{power: currentPower, err: err}
			continue
		}

		currentPower = command.nextPower
		log.Printf("dio: first port set to %s", currentPower)
		command.reply <- dioReply{power: currentPower}
	}
}

func handleDIOState(dioCommands chan<- dioCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		log.Printf("http: %s %s", request.Method, request.URL.Path)
		reply := make(chan dioReply, 1)
		dioCommands <- dioCommand{reply: reply}
		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(writer, map[string]string{"power": result.power})
	}
}

func handleDIOPower(dioCommands chan<- dioCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		log.Printf("http: %s %s", request.Method, request.URL.Path)
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var apiRequest struct {
			Power string `json:"power"`
		}
		if err := json.NewDecoder(request.Body).Decode(&apiRequest); err != nil {
			http.Error(writer, "invalid json", http.StatusBadRequest)
			return
		}

		reply := make(chan dioReply, 1)
		dioCommands <- dioCommand{nextPower: apiRequest.Power, reply: reply}
		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(writer, map[string]string{"power": result.power})
	}
}

func writeFirstDIOPower(dioValueFile string, nextPower string) error {
	if runtime.GOOS != "linux" {
		log.Printf("dio: non-linux runtime detected, keeping in-memory state only")
		return nil
	}

	nextValue := "0"
	if nextPower == "on" {
		nextValue = "1"
	}
	return os.WriteFile(dioValueFile, []byte(nextValue), 0o644)
}

func writeJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(payload)
}

func handleRequest(writer http.ResponseWriter, request *http.Request) {
	requestedFile := strings.TrimPrefix(request.URL.Path, "/")
	if requestedFile == "" {
		requestedFile = "index.html"
	}
	log.Printf("http: requested file=%s", requestedFile)

	fullPathToFile := *directoryFlag + "/" + requestedFile
	if fileExists(fullPathToFile) {
		log.Printf("http: serving local file=%s", fullPathToFile)
		http.ServeFile(writer, request, fullPathToFile)
		return
	}

	if fileExistsInStatic(requestedFile) {
		log.Printf("http: serving embedded file=%s", requestedFile)
		fileData, err := staticFiles.ReadFile(filepath.Join("static", requestedFile))
		if err != nil {
			http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		writer.Header().Set("Content-Type", getContentType(requestedFile))
		_, _ = writer.Write(fileData)
		return
	}

	http.NotFound(writer, request)
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return err == nil && !info.IsDir()
}

func fileExistsInStatic(filename string) bool {
	_, err := staticFiles.ReadFile(filepath.Join("static", filename))
	return err == nil
}

func getContentType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".html":
		return "text/html"
	case ".js":
		return "application/javascript"
	case ".css":
		return "text/css"
	default:
		return "application/octet-stream"
	}
}
