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
)

// =============================
// Embedded static assets.
// =============================

//go:embed static/*
var staticFiles embed.FS

var (
	portFlag                 = flag.Int("port", 8765, "web server port")
	directoryFlag            = flag.String("directory", ".", "directory to serve files from")
	dioValuePathTemplateFlag = flag.String("dio-value-path-template", "/sys/class/gpio/gpio%d/value", "DIO value file path template")
	labelsFileFlag           = flag.String("labels-file", "dio-labels.json", "path to labels file")
)

const portCount = 10

// =============================
// Domain model.
// =============================

type portState struct {
	Port  int    `json:"port"`
	Power string `json:"power"`
	Label string `json:"label"`
}

type appState struct {
	Ports []portState `json:"ports"`
}

type setPowerRequest struct {
	Port  int    `json:"port"`
	Power string `json:"power"`
}

type setLabelRequest struct {
	Port  int    `json:"port"`
	Label string `json:"label"`
}

type stateCommand struct {
	kind  string
	port  int
	power string
	label string
	reply chan stateReply
}

type stateReply struct {
	state appState
	err   error
}

// =============================
// Main entry point.
// =============================

func main() {
	flag.Parse()

	stateCommands := make(chan stateCommand)
	go runStateOwner(stateCommands, *dioValuePathTemplateFlag, *labelsFileFlag)

	http.HandleFunc("/api/state", handleGetState(stateCommands))
	http.HandleFunc("/api/power", handleSetPower(stateCommands))
	http.HandleFunc("/api/label", handleSetLabel(stateCommands))
	http.HandleFunc("/", handleRequest)

	address := fmt.Sprintf(":%d", *portFlag)
	log.Printf("startup: starting HTTP server on http://localhost%s", address)
	go func() {
		err := http.ListenAndServe(address, nil)
		if err != nil {
			log.Printf("shutdown: HTTP server stopped: %v", err)
		}
	}()

	runUserInterface(address)
}

// =============================
// State owner goroutine.
// =============================

func runStateOwner(stateCommands <-chan stateCommand, dioValuePathTemplate string, labelsFile string) {
	state := buildInitialState(loadLabels(labelsFile))
	log.Printf("state: owner started with %d ports", len(state.Ports))

	for command := range stateCommands {
		switch command.kind {
		case "get":
			command.reply <- stateReply{state: cloneState(state)}
		case "set_power":
			resultState, err := applyPower(state, command.port, command.power, dioValuePathTemplate)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			state = resultState
			command.reply <- stateReply{state: cloneState(state)}
		case "set_label":
			resultState, err := applyLabel(state, command.port, command.label)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			if err := saveLabels(labelsFile, resultState); err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			state = resultState
			command.reply <- stateReply{state: cloneState(state)}
		default:
			command.reply <- stateReply{state: cloneState(state), err: errors.New("unknown command")}
		}
	}
}

func buildInitialState(savedLabels map[int]string) appState {
	ports := make([]portState, 0, portCount)
	for portIndex := 1; portIndex <= portCount; portIndex++ {
		label := savedLabels[portIndex]
		if label == "" {
			label = fmt.Sprintf("DIO %d", portIndex)
		}
		ports = append(ports, portState{Port: portIndex, Power: "off", Label: label})
	}
	return appState{Ports: ports}
}

func applyPower(state appState, port int, nextPower string, dioValuePathTemplate string) (appState, error) {
	if port < 1 || port > portCount {
		return state, errors.New("invalid port")
	}
	if nextPower != "on" && nextPower != "off" {
		return state, errors.New("power must be on or off")
	}

	if err := writeDIOPower(port, nextPower, dioValuePathTemplate); err != nil {
		return state, err
	}

	nextState := cloneState(state)
	nextState.Ports[port-1].Power = nextPower
	log.Printf("dio: port=%d power=%s", port, nextPower)
	return nextState, nil
}

func applyLabel(state appState, port int, nextLabel string) (appState, error) {
	if port < 1 || port > portCount {
		return state, errors.New("invalid port")
	}
	sanitizedLabel := strings.TrimSpace(nextLabel)
	if sanitizedLabel == "" {
		return state, errors.New("label is required")
	}

	nextState := cloneState(state)
	nextState.Ports[port-1].Label = sanitizedLabel
	log.Printf("dio: port=%d label=%s", port, sanitizedLabel)
	return nextState, nil
}

func cloneState(source appState) appState {
	copiedPorts := make([]portState, len(source.Ports))
	copy(copiedPorts, source.Ports)
	return appState{Ports: copiedPorts}
}

func writeDIOPower(port int, nextPower string, dioValuePathTemplate string) error {
	if runtime.GOOS != "linux" {
		log.Printf("dio: non-linux runtime, skip physical write for port=%d", port)
		return nil
	}

	nextValue := "0"
	if nextPower == "on" {
		nextValue = "1"
	}
	path := fmt.Sprintf(dioValuePathTemplate, port)
	return os.WriteFile(path, []byte(nextValue), 0o644)
}

func loadLabels(labelsFile string) map[int]string {
	fileData, err := os.ReadFile(labelsFile)
	if err != nil {
		return map[int]string{}
	}

	var labels map[int]string
	if err := json.Unmarshal(fileData, &labels); err != nil {
		return map[int]string{}
	}
	return labels
}

func saveLabels(labelsFile string, state appState) error {
	labels := map[int]string{}
	for _, singlePortState := range state.Ports {
		labels[singlePortState.Port] = singlePortState.Label
	}
	fileData, err := json.MarshalIndent(labels, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(labelsFile, fileData, 0o644)
}

// =============================
// HTTP handlers.
// =============================

func handleGetState(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		log.Printf("http: %s %s", request.Method, request.URL.Path)
		reply := make(chan stateReply, 1)
		stateCommands <- stateCommand{kind: "get", reply: reply}
		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(writer, result.state)
	}
}

func handleSetPower(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		log.Printf("http: %s %s", request.Method, request.URL.Path)
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var apiRequest setPowerRequest
		if err := json.NewDecoder(request.Body).Decode(&apiRequest); err != nil {
			http.Error(writer, "invalid json", http.StatusBadRequest)
			return
		}

		reply := make(chan stateReply, 1)
		stateCommands <- stateCommand{kind: "set_power", port: apiRequest.Port, power: apiRequest.Power, reply: reply}
		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(writer, result.state)
	}
}

func handleSetLabel(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		log.Printf("http: %s %s", request.Method, request.URL.Path)
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var apiRequest setLabelRequest
		if err := json.NewDecoder(request.Body).Decode(&apiRequest); err != nil {
			http.Error(writer, "invalid json", http.StatusBadRequest)
			return
		}

		reply := make(chan stateReply, 1)
		stateCommands <- stateCommand{kind: "set_label", port: apiRequest.Port, label: apiRequest.Label, reply: reply}
		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(writer, result.state)
	}
}

func writeJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(payload)
}

// =============================
// Static file serving.
// =============================

func handleRequest(writer http.ResponseWriter, request *http.Request) {
	requestedFile := strings.TrimPrefix(request.URL.Path, "/")
	if requestedFile == "" {
		requestedFile = "index.html"
	}

	fullPathToFile := filepath.Join(*directoryFlag, requestedFile)
	if fileExists(fullPathToFile) {
		http.ServeFile(writer, request, fullPathToFile)
		return
	}

	if fileExistsInStatic(requestedFile) {
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
