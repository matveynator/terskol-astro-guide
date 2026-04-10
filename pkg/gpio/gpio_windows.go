//go:build windows

package gpio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	vecowInitIsolateNonIsolated        = 0
	vecowInitDIONPN                    = 0
	vecowGPIOConfigMask         uint16 = 0xFF00
)

var vecowDLLCandidates = []string{
	"drv.dll",
	"WinRing0x64.dll",
	"OpenHardwareMonitorLib.dll",
	"Vecow.dll",
	"ECX1K.dll",
}

type windowsAdapter struct {
	dllName     string
	dll         *windows.LazyDLL
	procInitial *windows.LazyProc
	procConfig  *windows.LazyProc
	procGetGPIO *windows.LazyProc
	procSetGPIO *windows.LazyProc

	outputMask atomic.Uint32
}

func DefaultInputTemplate() string {
	return ""
}

func DefaultOutputTemplate() string {
	return ""
}

func Open(config Config) (Adapter, RuntimeMode, error) {
	_ = config

	adapter, mode, err := openWindowsAdapter()
	if err != nil {
		return nil, RuntimeMode{}, err
	}
	return adapter, mode, nil
}

func openWindowsAdapter() (*windowsAdapter, RuntimeMode, error) {
	searchOrder := windowsDLLSearchOrder()
	driverProbeEvents := make([]string, 0, len(searchOrder)*7)
	for _, dllName := range searchOrder {
		adapter, dllProbeEvents, err := tryOpenWindowsAdapter(dllName)
		driverProbeEvents = append(driverProbeEvents, dllProbeEvents...)
		if err != nil {
			continue
		}

		driverProbeEvents = append(driverProbeEvents, fmt.Sprintf("GPIO ports are available. Active DLL: %s", filepath.Base(dllName)))
		driverProbeEvents = append(driverProbeEvents, "Stop probing next DLL files.")
		return adapter, RuntimeMode{
			ActiveDriver:   filepath.Base(dllName),
			DriverProbeLog: strings.Join(driverProbeEvents, "\n"),
		}, nil
	}

	return nil, RuntimeMode{}, fmt.Errorf("initialize Vecow GPIO failed:\n%s", strings.Join(driverProbeEvents, "\n"))
}

func windowsDLLSearchOrder() []string {
	driverDir := strings.TrimSpace(os.Getenv("CHICHA_GPIO_WINDOWS_DRIVER_DIR"))
	customDLL := strings.TrimSpace(os.Getenv("CHICHA_GPIO_WINDOWS_DLL"))

	searchOrder := make([]string, 0, len(vecowDLLCandidates)+2)
	if customDLL != "" {
		searchOrder = append(searchOrder, customDLL)
	}
	for _, dllName := range vecowDLLCandidates {
		if driverDir != "" {
			searchOrder = append(searchOrder, filepath.Join(driverDir, dllName))
		}
		searchOrder = append(searchOrder, dllName)
	}
	return searchOrder
}

func tryOpenWindowsAdapter(dllName string) (*windowsAdapter, []string, error) {
	dllPathForLog := resolveDLLPathForLog(dllName)
	probeEvents := []string{fmt.Sprintf("Try DLL: %s", dllPathForLog)}

	dll := windows.NewLazyDLL(dllName)
	if err := dll.Load(); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  Load DLL: FAIL (%v)", err))
		return nil, probeEvents, fmt.Errorf("load %s: %w", dllName, err)
	}
	probeEvents = append(probeEvents, "  Load DLL: OK")

	adapter := &windowsAdapter{
		dllName:     dllName,
		dll:         dll,
		procInitial: dll.NewProc("Initial"),
		procConfig:  dll.NewProc("SetGPIOConfig"),
		procGetGPIO: dll.NewProc("GetGPIO"),
		procSetGPIO: dll.NewProc("SetGPIO"),
	}

	if err := adapter.procInitial.Find(); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  Resolve Initial: FAIL (%v)", err))
		logDLLReleaseResult(adapter.dll, &probeEvents)
		return nil, probeEvents, fmt.Errorf("resolve Initial in %s: %w", dllName, err)
	}
	probeEvents = append(probeEvents, "  Resolve Initial: OK")
	if err := adapter.procConfig.Find(); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  Resolve SetGPIOConfig: FAIL (%v)", err))
		logDLLReleaseResult(adapter.dll, &probeEvents)
		return nil, probeEvents, fmt.Errorf("resolve SetGPIOConfig in %s: %w", dllName, err)
	}
	probeEvents = append(probeEvents, "  Resolve SetGPIOConfig: OK")
	if err := adapter.procGetGPIO.Find(); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  Resolve GetGPIO: FAIL (%v)", err))
		logDLLReleaseResult(adapter.dll, &probeEvents)
		return nil, probeEvents, fmt.Errorf("resolve GetGPIO in %s: %w", dllName, err)
	}
	probeEvents = append(probeEvents, "  Resolve GetGPIO: OK")
	if err := adapter.procSetGPIO.Find(); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  Resolve SetGPIO: FAIL (%v)", err))
		logDLLReleaseResult(adapter.dll, &probeEvents)
		return nil, probeEvents, fmt.Errorf("resolve SetGPIO in %s: %w", dllName, err)
	}
	probeEvents = append(probeEvents, "  Resolve SetGPIO: OK")

	if err := adapter.callInitial(); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  Initial call: FAIL (%v)", err))
		logDLLReleaseResult(adapter.dll, &probeEvents)
		return nil, probeEvents, err
	}
	probeEvents = append(probeEvents, "  Initial call: OK")
	if err := adapter.callSetGPIOConfig(vecowGPIOConfigMask); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  SetGPIOConfig call: FAIL (%v)", err))
		logDLLReleaseResult(adapter.dll, &probeEvents)
		return nil, probeEvents, err
	}
	probeEvents = append(probeEvents, "  SetGPIOConfig call: OK")
	if err := adapter.callSetGPIO(0); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  SetGPIO call: FAIL (%v)", err))
		logDLLReleaseResult(adapter.dll, &probeEvents)
		return nil, probeEvents, err
	}
	probeEvents = append(probeEvents, "  SetGPIO call: OK")

	var gpioState uint16
	if err := adapter.callGetGPIO(&gpioState); err != nil {
		probeEvents = append(probeEvents, fmt.Sprintf("  GPIO ports check: FAIL (%v)", err))
		logDLLReleaseResult(adapter.dll, &probeEvents)
		return nil, probeEvents, fmt.Errorf("GPIO ports unavailable in %s: %w", dllName, err)
	}
	probeEvents = append(probeEvents, fmt.Sprintf("  GPIO ports check: OK (state=0x%04X)", gpioState))
	probeEvents = append(probeEvents, "  Keep DLL loaded for runtime mode.")

	adapter.outputMask.Store(0)
	return adapter, probeEvents, nil
}

func resolveDLLPathForLog(dllName string) string {
	if filepath.IsAbs(dllName) {
		return dllName
	}

	if strings.ContainsRune(dllName, os.PathSeparator) {
		absolutePath, err := filepath.Abs(dllName)
		if err != nil {
			return dllName
		}
		return absolutePath
	}

	return fmt.Sprintf("PATH lookup: %s", dllName)
}

func logDLLReleaseResult(dll *windows.LazyDLL, probeEvents *[]string) {
	if err := releaseLazyDLL(dll); err != nil {
		*probeEvents = append(*probeEvents, fmt.Sprintf("  Unload DLL: FAIL (%v)", err))
		return
	}
	*probeEvents = append(*probeEvents, "  Unload DLL: OK")
}

func (adapter *windowsAdapter) ReadInput(channel int) (bool, error) {
	if channel < 1 || channel > InputCount {
		return false, fmt.Errorf("invalid input channel %d", channel)
	}

	var state uint16
	if err := adapter.callGetGPIO(&state); err != nil {
		return false, err
	}
	bitMask := uint16(1 << (channel - 1))
	return state&bitMask != 0, nil
}

func (adapter *windowsAdapter) WriteOutput(channel int, high bool) error {
	if channel < 1 || channel > OutputCount {
		return fmt.Errorf("invalid output channel %d", channel)
	}

	bitMask := uint32(1 << uint(channel+7))
	for {
		currentMask := adapter.outputMask.Load()
		nextMask := currentMask
		if high {
			nextMask |= bitMask
		} else {
			nextMask &^= bitMask
		}

		if !adapter.outputMask.CompareAndSwap(currentMask, nextMask) {
			continue
		}

		if err := adapter.callSetGPIO(uint16(nextMask)); err != nil {
			_ = adapter.outputMask.CompareAndSwap(nextMask, currentMask)
			return err
		}
		return nil
	}
}

func (adapter *windowsAdapter) Close() error {
	if adapter.dll == nil {
		return nil
	}
	if err := releaseLazyDLL(adapter.dll); err != nil {
		return fmt.Errorf("release %s: %w", adapter.dllName, err)
	}
	adapter.dll = nil
	return nil
}

func releaseLazyDLL(dll *windows.LazyDLL) error {
	if dll == nil {
		return nil
	}

	handle := dll.Handle()
	if handle == 0 {
		return nil
	}

	return windows.FreeLibrary(windows.Handle(handle))
}

func (adapter *windowsAdapter) callInitial() error {
	result, _, _ := adapter.procInitial.Call(vecowInitIsolateNonIsolated, vecowInitDIONPN)
	if result != 0 {
		return fmt.Errorf("Initial returned %d", result)
	}
	return nil
}

func (adapter *windowsAdapter) callSetGPIOConfig(mask uint16) error {
	result, _, _ := adapter.procConfig.Call(uintptr(mask))
	if result != 0 {
		return fmt.Errorf("SetGPIOConfig returned %d", result)
	}
	return nil
}

func (adapter *windowsAdapter) callSetGPIO(mask uint16) error {
	result, _, _ := adapter.procSetGPIO.Call(uintptr(mask))
	if result != 0 {
		return fmt.Errorf("SetGPIO returned %d", result)
	}
	return nil
}

func (adapter *windowsAdapter) callGetGPIO(state *uint16) error {
	result, _, _ := adapter.procGetGPIO.Call(uintptr(unsafe.Pointer(state)))
	if result != 0 {
		return fmt.Errorf("GetGPIO returned %d", result)
	}
	return nil
}
