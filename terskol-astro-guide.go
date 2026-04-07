package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	webview "github.com/webview/webview_go"
)

// =============================
// Embedded static assets.
// =============================

//go:embed static/*
var embeddedStaticFiles embed.FS

var (
	portFlag      = flag.Int("port", 8765, "web server port")
	directoryFlag = flag.String("directory", ".", "directory to serve files from")
)

// =============================
// Domain model and contracts.
// =============================

type DeviceID string

type DeviceType string

type PowerState string

const (
	DeviceSocket1 DeviceID   = "socket-1"
	DeviceTypeDIO DeviceType = "dio_5v_output"
	PowerStateOff PowerState = "off"
	PowerStateOn  PowerState = "on"
)

type DeviceState struct {
	ID    DeviceID   `json:"id"`
	Type  DeviceType `json:"type"`
	Power PowerState `json:"power"`
}

type AppSnapshot struct {
	Devices []DeviceState `json:"devices"`
}

type DIOBackend interface {
	SetDIOPort(ctx context.Context, deviceID DeviceID, turnOn bool) error
	ReadDIOPort(ctx context.Context, deviceID DeviceID) (bool, error)
}

// =============================
// In-memory DIO backend.
// =============================

type MemoryDIOBackend struct {
	stateByDevice map[DeviceID]bool
}

func NewMemoryDIOBackend() *MemoryDIOBackend {
	return &MemoryDIOBackend{stateByDevice: map[DeviceID]bool{DeviceSocket1: false}}
}

func (backend *MemoryDIOBackend) SetDIOPort(_ context.Context, deviceID DeviceID, turnOn bool) error {
	if _, exists := backend.stateByDevice[deviceID]; !exists {
		return fmt.Errorf("unknown device: %s", deviceID)
	}
	backend.stateByDevice[deviceID] = turnOn
	return nil
}

func (backend *MemoryDIOBackend) ReadDIOPort(_ context.Context, deviceID DeviceID) (bool, error) {
	state, exists := backend.stateByDevice[deviceID]
	if !exists {
		return false, fmt.Errorf("unknown device: %s", deviceID)
	}
	return state, nil
}

// =============================
// Relay manager goroutine.
// =============================

type relayCommandType int

const (
	relayCommandSnapshot relayCommandType = iota
	relayCommandSetPower
)

type relayCommand struct {
	commandType relayCommandType
	deviceID    DeviceID
	turnOn      bool
	reply       chan relayReply
}

type relayReply struct {
	snapshot AppSnapshot
	err      error
}

func runRelayManager(ctx context.Context, backend DIOBackend, commands <-chan relayCommand) {
	stateByDevice := map[DeviceID]DeviceState{
		DeviceSocket1: {
			ID:    DeviceSocket1,
			Type:  DeviceTypeDIO,
			Power: PowerStateOff,
		},
	}

	for {
		select {
		case <-ctx.Done():
			return
		case command := <-commands:
			switch command.commandType {
			case relayCommandSnapshot:
				command.reply <- relayReply{snapshot: makeSnapshot(stateByDevice)}
			case relayCommandSetPower:
				err := backend.SetDIOPort(ctx, command.deviceID, command.turnOn)
				if err != nil {
					command.reply <- relayReply{err: err}
					continue
				}

				confirmedState, err := backend.ReadDIOPort(ctx, command.deviceID)
				if err != nil {
					command.reply <- relayReply{err: err}
					continue
				}

				deviceState := stateByDevice[command.deviceID]
				if confirmedState {
					deviceState.Power = PowerStateOn
				} else {
					deviceState.Power = PowerStateOff
				}
				stateByDevice[command.deviceID] = deviceState
				command.reply <- relayReply{snapshot: makeSnapshot(stateByDevice)}
			}
		}
	}
}

func makeSnapshot(stateByDevice map[DeviceID]DeviceState) AppSnapshot {
	return AppSnapshot{Devices: []DeviceState{stateByDevice[DeviceSocket1]}}
}

// =============================
// API handlers.
// =============================

type setPowerRequest struct {
	TurnOn bool `json:"turn_on"`
}

func getSnapshotHandler(commands chan<- relayCommand) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, _ *http.Request) {
		reply := make(chan relayReply, 1)
		commands <- relayCommand{commandType: relayCommandSnapshot, reply: reply}
		result := <-reply
		if result.err != nil {
			http.Error(responseWriter, result.err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(responseWriter, result.snapshot)
	}
}

func setPowerHandler(commands chan<- relayCommand) http.HandlerFunc {
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

		reply := make(chan relayReply, 1)
		commands <- relayCommand{commandType: relayCommandSetPower, deviceID: DeviceSocket1, turnOn: apiRequest.TurnOn, reply: reply}
		result := <-reply
		if result.err != nil {
			http.Error(responseWriter, result.err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(responseWriter, result.snapshot)
	}
}

func writeJSON(responseWriter http.ResponseWriter, payload any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(responseWriter).Encode(payload); err != nil {
		http.Error(responseWriter, err.Error(), http.StatusInternalServerError)
	}
}

// =============================
// HTTP static file handling.
// =============================

func staticHandler(responseWriter http.ResponseWriter, request *http.Request) {
	requestedFile := strings.TrimPrefix(request.URL.Path, "/")
	if requestedFile == "" {
		requestedFile = "index.html"
	}

	fullPathToFile := filepath.Join(*directoryFlag, requestedFile)
	if fileExists(fullPathToFile) {
		http.ServeFile(responseWriter, request, fullPathToFile)
		return
	}

	if fileExistsInEmbeddedStatic(requestedFile) {
		embeddedFileData, err := embeddedStaticFiles.ReadFile(filepath.Join("static", requestedFile))
		if err != nil {
			http.Error(responseWriter, "internal server error", http.StatusInternalServerError)
			return
		}
		responseWriter.Header().Set("Content-Type", getContentType(requestedFile))
		_, _ = responseWriter.Write(embeddedFileData)
		return
	}

	http.NotFound(responseWriter, request)
}

func fileExists(filename string) bool {
	fileInfo, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return err == nil && !fileInfo.IsDir()
}

func fileExistsInEmbeddedStatic(filename string) bool {
	_, err := fs.Stat(embeddedStaticFiles, filepath.Join("static", filename))
	return err == nil
}

func getContentType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "application/javascript"
	case ".css":
		return "text/css"
	default:
		return "application/octet-stream"
	}
}

// =============================
// Application bootstrap.
// =============================

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := NewMemoryDIOBackend()
	commands := make(chan relayCommand)
	go runRelayManager(ctx, backend, commands)

	http.HandleFunc("/api/snapshot", getSnapshotHandler(commands))
	http.HandleFunc("/api/power", setPowerHandler(commands))
	http.HandleFunc("/", staticHandler)

	address := fmt.Sprintf(":%d", *portFlag)
	go func() {
		if err := http.ListenAndServe(address, nil); err != nil {
			log.Printf("http server stopped: %v", err)
		}
	}()

	window := webview.New(false)
	if window == nil {
		log.Fatal("failed to create webview window")
	}
	defer window.Destroy()

	window.SetTitle("DIO 1 Socket Control")
	window.SetSize(920, 700, webview.HintNone)
	window.Navigate("http://localhost" + address)
	window.Run()

	// runtime.KeepAlive prevents important references from being finalized before shutdown.
	runtime.KeepAlive(backend)
	time.Sleep(10 * time.Millisecond)
}
