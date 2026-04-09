//go:build windows

package gpio

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const (
	vecowInitIsolateNonIsolated        = 0
	vecowInitDIONPN                    = 0
	vecowOutputMask             uint16 = 0xFF00
)

type windowsAdapter struct {
	dllHandle syscall.Handle
	procInit  uintptr
	procCfg   uintptr
	procGet   uintptr
	procSet   uintptr

	outputMask atomic.Uint32
}

func DefaultInputTemplate() string {
	return `C:\Vecow\ECX1K\di%d.value`
}

func DefaultOutputTemplate() string {
	return `C:\Vecow\ECX1K\do%d.value`
}

func Open(config Config) (Adapter, RuntimeMode, error) {
	_ = config
	adapter, err := openWindowsDLLAdapter()
	if err != nil {
		return SimulationAdapter{}, RuntimeMode{InputSimulation: true, OutputSimulation: true}, nil
	}
	return adapter, RuntimeMode{}, nil
}

func openWindowsDLLAdapter() (*windowsAdapter, error) {
	handle, err := syscall.LoadLibrary("ECX1K.dll")
	if err != nil {
		return nil, err
	}

	loadProc := func(name string) (uintptr, error) {
		procAddr, procErr := syscall.GetProcAddress(handle, name)
		if procErr != nil {
			_ = syscall.FreeLibrary(handle)
			return 0, fmt.Errorf("resolve %s: %w", name, procErr)
		}
		return uintptr(procAddr), nil
	}

	procInit, err := loadProc("Initial")
	if err != nil {
		return nil, err
	}
	procCfg, err := loadProc("SetGPIOConfig")
	if err != nil {
		return nil, err
	}
	procGet, err := loadProc("GetGPIO")
	if err != nil {
		return nil, err
	}
	procSet, err := loadProc("SetGPIO")
	if err != nil {
		return nil, err
	}

	adapter := &windowsAdapter{
		dllHandle: syscall.Handle(handle),
		procInit:  procInit,
		procCfg:   procCfg,
		procGet:   procGet,
		procSet:   procSet,
	}

	if callErr := adapter.callInitial(); callErr != nil {
		_ = syscall.FreeLibrary(handle)
		return nil, callErr
	}
	if callErr := adapter.callSetGPIOConfig(vecowOutputMask); callErr != nil {
		_ = syscall.FreeLibrary(handle)
		return nil, callErr
	}
	adapter.outputMask.Store(0)
	if callErr := adapter.callSetGPIO(0); callErr != nil {
		_ = syscall.FreeLibrary(handle)
		return nil, callErr
	}

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
	mask := uint16(1 << (channel - 1))
	return state&mask != 0, nil
}

func (adapter *windowsAdapter) WriteOutput(channel int, high bool) error {
	if channel < 1 || channel > OutputCount {
		return fmt.Errorf("invalid output channel %d", channel)
	}
	bitIndex := uint(channel + 7)
	bitMask := uint32(1 << bitIndex)

	for {
		currentMask := adapter.outputMask.Load()
		nextMask := currentMask
		if high {
			nextMask |= bitMask
		} else {
			nextMask &^= bitMask
		}

		if adapter.outputMask.CompareAndSwap(currentMask, nextMask) {
			return adapter.callSetGPIO(uint16(nextMask))
		}
		runtime.Gosched()
	}
}

func (adapter *windowsAdapter) Close() error {
	if adapter.dllHandle == 0 {
		return nil
	}
	handle := adapter.dllHandle
	adapter.dllHandle = 0
	return syscall.FreeLibrary(handle)
}

func (adapter *windowsAdapter) callInitial() error {
	result, _, _ := syscall.SyscallN(adapter.procInit, vecowInitIsolateNonIsolated, vecowInitDIONPN)
	if result != 0 {
		return fmt.Errorf("Initial returned %d", result)
	}
	return nil
}

func (adapter *windowsAdapter) callSetGPIOConfig(mask uint16) error {
	result, _, _ := syscall.SyscallN(adapter.procCfg, uintptr(mask))
	if result != 0 {
		return fmt.Errorf("SetGPIOConfig returned %d", result)
	}
	return nil
}

func (adapter *windowsAdapter) callSetGPIO(mask uint16) error {
	result, _, _ := syscall.SyscallN(adapter.procSet, uintptr(mask))
	if result != 0 {
		return fmt.Errorf("SetGPIO returned %d", result)
	}
	return nil
}

func (adapter *windowsAdapter) callGetGPIO(state *uint16) error {
	result, _, _ := syscall.SyscallN(adapter.procGet, uintptr(unsafe.Pointer(state)))
	if result != 0 {
		return fmt.Errorf("GetGPIO returned %d", result)
	}
	return nil
}
