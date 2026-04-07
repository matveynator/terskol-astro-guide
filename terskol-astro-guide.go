package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"runtime"
	"time"

	webview "github.com/webview/webview_go"
)

// =============================
// Domain model and contracts.
// =============================

// DeviceID keeps identifiers explicit so adding new DIO devices stays predictable.
type DeviceID string

const (
	DeviceSocket1 DeviceID = "socket-1"
)

// DeviceType reserves room for future DIO devices (relays, lamps, motors, etc.).
type DeviceType string

const (
	DeviceTypeRelaySocket DeviceType = "relay_socket"
)

// PowerState is explicit for easier JSON transfer and UI rendering.
type PowerState string

const (
	PowerStateOff PowerState = "off"
	PowerStateOn  PowerState = "on"
)

// DeviceState is the single source of truth transferred between backend and UI.
type DeviceState struct {
	ID    DeviceID   `json:"id"`
	Type  DeviceType `json:"type"`
	Power PowerState `json:"power"`
}

// AppSnapshot can be extended with multiple devices without API breakage.
type AppSnapshot struct {
	Devices []DeviceState `json:"devices"`
}

// DIOBackend isolates platform specifics from the relay business flow.
type DIOBackend interface {
	SetRelay(ctx context.Context, deviceID DeviceID, turnOn bool) error
	ReadRelay(ctx context.Context, deviceID DeviceID) (bool, error)
}

// =============================
// In-memory DIO backend.
// =============================

// MemoryDIOBackend provides deterministic behavior for MVP and desktop testing.
type MemoryDIOBackend struct {
	stateByDevice map[DeviceID]bool
}

func NewMemoryDIOBackend() *MemoryDIOBackend {
	return &MemoryDIOBackend{
		stateByDevice: map[DeviceID]bool{
			DeviceSocket1: false,
		},
	}
}

func (backend *MemoryDIOBackend) SetRelay(_ context.Context, deviceID DeviceID, turnOn bool) error {
	if _, exists := backend.stateByDevice[deviceID]; !exists {
		return fmt.Errorf("unknown device: %s", deviceID)
	}
	backend.stateByDevice[deviceID] = turnOn
	return nil
}

func (backend *MemoryDIOBackend) ReadRelay(_ context.Context, deviceID DeviceID) (bool, error) {
	isOn, exists := backend.stateByDevice[deviceID]
	if !exists {
		return false, fmt.Errorf("unknown device: %s", deviceID)
	}
	return isOn, nil
}

// =============================
// Relay manager goroutine.
// =============================

// relayCommandType keeps commands finite and explicit for select-based processing.
type relayCommandType int

const (
	relayCommandSnapshot relayCommandType = iota
	relayCommandSetPower
	relayCommandShutdown
)

// relayCommand is the only way to mutate/read relay state, so mutex is unnecessary.
type relayCommand struct {
	commandType relayCommandType
	deviceID    DeviceID
	turnOn      bool
	reply       chan relayReply
}

// relayReply unifies success and error paths for channel communication.
type relayReply struct {
	snapshot AppSnapshot
	err      error
}

func runRelayManager(ctx context.Context, backend DIOBackend, commands <-chan relayCommand) {
	stateByDevice := map[DeviceID]DeviceState{
		DeviceSocket1: {
			ID:    DeviceSocket1,
			Type:  DeviceTypeRelaySocket,
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
				err := backend.SetRelay(ctx, command.deviceID, command.turnOn)
				if err != nil {
					command.reply <- relayReply{err: err}
					continue
				}

				confirmedState, err := backend.ReadRelay(ctx, command.deviceID)
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
			case relayCommandShutdown:
				command.reply <- relayReply{}
				return
			}
		}
	}
}

func makeSnapshot(stateByDevice map[DeviceID]DeviceState) AppSnapshot {
	orderedDevices := []DeviceState{stateByDevice[DeviceSocket1]}
	return AppSnapshot{Devices: orderedDevices}
}

// =============================
// WebView bridge.
// =============================

// UIBridge exposes a narrow RPC API to JavaScript.
type UIBridge struct {
	commands chan<- relayCommand
}

func NewUIBridge(commands chan<- relayCommand) *UIBridge {
	return &UIBridge{commands: commands}
}

func (bridge *UIBridge) GetSnapshot() (string, error) {
	reply := make(chan relayReply, 1)
	bridge.commands <- relayCommand{commandType: relayCommandSnapshot, reply: reply}
	result := <-reply
	if result.err != nil {
		return "", result.err
	}
	return snapshotToJSON(result.snapshot)
}

func (bridge *UIBridge) SetSocket1Power(turnOn bool) (string, error) {
	reply := make(chan relayReply, 1)
	bridge.commands <- relayCommand{
		commandType: relayCommandSetPower,
		deviceID:    DeviceSocket1,
		turnOn:      turnOn,
		reply:       reply,
	}
	result := <-reply
	if result.err != nil {
		return "", result.err
	}
	return snapshotToJSON(result.snapshot)
}

func snapshotToJSON(snapshot AppSnapshot) (string, error) {
	encodedSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return string(encodedSnapshot), nil
}

// =============================
// Embedded UI markup.
// =============================

const htmlPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Terskol Relay Control</title>
  <style>
    body {
      margin: 0;
      font-family: Arial, sans-serif;
      background: #111827;
      color: #f9fafb;
      display: flex;
      justify-content: center;
      align-items: center;
      min-height: 100vh;
    }
    .panel {
      width: 420px;
      background: #1f2937;
      border-radius: 12px;
      padding: 24px;
      box-shadow: 0 8px 30px rgba(0,0,0,0.35);
    }
    h1 {
      margin-top: 0;
      font-size: 24px;
    }
    .status {
      margin: 14px 0;
      font-size: 16px;
    }
    .status strong {
      color: #93c5fd;
    }
    .row {
      display: flex;
      gap: 12px;
    }
    button {
      flex: 1;
      border: 0;
      border-radius: 10px;
      padding: 12px;
      color: #f9fafb;
      font-size: 15px;
      cursor: pointer;
    }
    button:disabled {
      opacity: 0.6;
      cursor: not-allowed;
    }
    .on {
      background: #059669;
    }
    .off {
      background: #dc2626;
    }
    .error {
      margin-top: 12px;
      color: #fca5a5;
      min-height: 20px;
    }
  </style>
</head>
<body>
  <main class="panel">
    <h1>Socket Control</h1>
    <p class="status">State: <strong id="power-state">unknown</strong></p>
    <div class="row">
      <button id="button-on" class="on">Turn ON</button>
      <button id="button-off" class="off">Turn OFF</button>
    </div>
    <p id="error-message" class="error"></p>
  </main>

  <script>
    const model = {
      devicesByID: {},
      isRequestActive: false,
      errorText: ""
    };

    const socketID = "socket-1";

    const powerStateElement = document.getElementById("power-state");
    const buttonOnElement = document.getElementById("button-on");
    const buttonOffElement = document.getElementById("button-off");
    const errorMessageElement = document.getElementById("error-message");

    function renderUIFromModel() {
      const deviceState = model.devicesByID[socketID];
      if (!deviceState) {
        powerStateElement.textContent = "unknown";
      } else {
        powerStateElement.textContent = deviceState.power;
      }

      buttonOnElement.disabled = model.isRequestActive;
      buttonOffElement.disabled = model.isRequestActive;
      errorMessageElement.textContent = model.errorText;
    }

    function updateModelFromSnapshotJSON(snapshotJSON) {
      const snapshot = JSON.parse(snapshotJSON);
      const updatedDevicesByID = {};

      for (const deviceState of snapshot.devices) {
        updatedDevicesByID[deviceState.id] = deviceState;
      }

      model.devicesByID = updatedDevicesByID;
      model.errorText = "";
      renderUIFromModel();
    }

    async function refreshSnapshot() {
      try {
        const snapshotJSON = await bridge.GetSnapshot();
        updateModelFromSnapshotJSON(snapshotJSON);
      } catch (error) {
        model.errorText = String(error);
        renderUIFromModel();
      }
    }

    async function setPowerState(turnOn) {
      model.isRequestActive = true;
      model.errorText = "";
      renderUIFromModel();

      try {
        const snapshotJSON = await bridge.SetSocket1Power(turnOn);
        updateModelFromSnapshotJSON(snapshotJSON);
      } catch (error) {
        model.errorText = String(error);
      } finally {
        model.isRequestActive = false;
        renderUIFromModel();
      }
    }

    buttonOnElement.addEventListener("click", function onClickTurnOn() {
      void setPowerState(true);
    });

    buttonOffElement.addEventListener("click", function onClickTurnOff() {
      void setPowerState(false);
    });

    void refreshSnapshot();
  </script>
</body>
</html>`

// =============================
// Application bootstrap.
// =============================

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := NewMemoryDIOBackend()
	commands := make(chan relayCommand)
	go runRelayManager(ctx, backend, commands)

	uiBridge := NewUIBridge(commands)

	debugMode := false
	window := webview.New(debugMode)
	if window == nil {
		log.Fatal("failed to create webview window")
	}
	defer window.Destroy()

	window.SetTitle("Terskol Astro Guide - Relay")
	window.SetSize(600, 420, webview.HintNone)

	if err := window.Bind("bridge", uiBridge); err != nil {
		log.Fatalf("failed to bind UI bridge: %v", err)
	}

	pageURL := "data:text/html," + url.PathEscape(htmlPage)
	window.Navigate(pageURL)
	window.Run()

	shutdownReply := make(chan relayReply, 1)
	commands <- relayCommand{commandType: relayCommandShutdown, reply: shutdownReply}
	result := <-shutdownReply
	if result.err != nil && !errors.Is(result.err, context.Canceled) {
		log.Printf("relay manager shutdown warning: %v", result.err)
	}

	// runtime.KeepAlive prevents aggressive optimizers from collecting bridge references early.
	runtime.KeepAlive(uiBridge)
	time.Sleep(10 * time.Millisecond)
}
