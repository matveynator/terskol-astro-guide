//go:build windows

package gpio

import (
	"fmt"
	"os"
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
	"ECX1K.dll",
	"Vecow.dll",
	"drv.dll",
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

	adapter, err := openWindowsAdapter()
	if err != nil {
		return nil, RuntimeMode{}, err
	}
	return adapter, RuntimeMode{}, nil
}

func openWindowsAdapter() (*windowsAdapter, error) {
	for _, dllName := range windowsDLLSearchOrder() {
		adapter, err := tryOpenWindowsAdapter(dllName)
		if err == nil {
			return adapter, nil
		}
	}

	return nil, fmt.Errorf(
		"initialize Vecow GPIO failed: none of DLLs can be used (%s)",
		strings.Join(windowsDLLSearchOrder(), ", "),
	)
}

func windowsDLLSearchOrder() []string {
	customDLL := strings.TrimSpace(os.Getenv("CHICHA_GPIO_WINDOWS_DLL"))
	if customDLL == "" {
		return vecowDLLCandidates
	}

	searchOrder := make([]string, 0, len(vecowDLLCandidates)+1)
	searchOrder = append(searchOrder, customDLL)
	searchOrder = append(searchOrder, vecowDLLCandidates...)
	return searchOrder
}

func tryOpenWindowsAdapter(dllName string) (*windowsAdapter, error) {
	dll := windows.NewLazyDLL(dllName)
	if err := dll.Load(); err != nil {
		return nil, fmt.Errorf("load %s: %w", dllName, err)
	}

	adapter := &windowsAdapter{
		dllName:     dllName,
		dll:         dll,
		procInitial: dll.NewProc("Initial"),
		procConfig:  dll.NewProc("SetGPIOConfig"),
		procGetGPIO: dll.NewProc("GetGPIO"),
		procSetGPIO: dll.NewProc("SetGPIO"),
	}

	if err := adapter.procInitial.Find(); err != nil {
		_ = releaseLazyDLL(adapter.dll)
		return nil, fmt.Errorf("resolve Initial in %s: %w", dllName, err)
	}
	if err := adapter.procConfig.Find(); err != nil {
		_ = releaseLazyDLL(adapter.dll)
		return nil, fmt.Errorf("resolve SetGPIOConfig in %s: %w", dllName, err)
	}
	if err := adapter.procGetGPIO.Find(); err != nil {
		_ = releaseLazyDLL(adapter.dll)
		return nil, fmt.Errorf("resolve GetGPIO in %s: %w", dllName, err)
	}
	if err := adapter.procSetGPIO.Find(); err != nil {
		_ = releaseLazyDLL(adapter.dll)
		return nil, fmt.Errorf("resolve SetGPIO in %s: %w", dllName, err)
	}

	if err := adapter.callInitial(); err != nil {
		_ = releaseLazyDLL(adapter.dll)
		return nil, err
	}
	if err := adapter.callSetGPIOConfig(vecowGPIOConfigMask); err != nil {
		_ = releaseLazyDLL(adapter.dll)
		return nil, err
	}
	if err := adapter.callSetGPIO(0); err != nil {
		_ = releaseLazyDLL(adapter.dll)
		return nil, err
	}
	adapter.outputMask.Store(0)

	return adapter, nil
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
