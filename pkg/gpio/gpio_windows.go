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
}

type windowsDriverProbeAttempt struct {
	dllPath string
	steps   []string
	outcome string
}

type windowsGPIOProbeTest struct {
	name             string
	initProcName     string
	getProcName      string
	setProcName      string
	configProcName   string
	isConfigOptional bool
}

type windowsDriverProbeError struct {
	summary  string
	probeLog string
}

func (probeErr *windowsDriverProbeError) Error() string {
	return probeErr.summary
}

func (probeErr *windowsDriverProbeError) ProbeLog() string {
	return probeErr.probeLog
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
	adapter, mode, err := openWindowsAdapter(config.WindowsDLLPath)
	if err != nil {
		return nil, RuntimeMode{}, err
	}
	return adapter, mode, nil
}

func openWindowsAdapter(overrideDLLPath string) (*windowsAdapter, RuntimeMode, error) {
	searchOrder := windowsDLLSearchOrder(overrideDLLPath)
	allProbeAttempts := make([]windowsDriverProbeAttempt, 0, len(searchOrder))
	for _, dllName := range searchOrder {
		adapter, probeAttempt, err := tryOpenWindowsAdapter(dllName)
		allProbeAttempts = append(allProbeAttempts, probeAttempt)
		if err != nil {
			continue
		}

		driverProbeLog := formatWindowsDriverProbeLog(allProbeAttempts)
		return adapter, RuntimeMode{
			ActiveDriver:   filepath.Base(dllName),
			DriverProbeLog: driverProbeLog,
		}, nil
	}

	driverProbeLog := formatWindowsDriverProbeLog(allProbeAttempts)
	return nil, RuntimeMode{}, &windowsDriverProbeError{
		summary:  "initialize Vecow GPIO failed: no suitable DLL found",
		probeLog: driverProbeLog,
	}
}

func windowsDLLSearchOrder(overrideDLLPath string) []string {
	driverDir := strings.TrimSpace(os.Getenv("CHICHA_GPIO_WINDOWS_DRIVER_DIR"))
	customDLL := strings.TrimSpace(os.Getenv("CHICHA_GPIO_WINDOWS_DLL"))
	overridePath := strings.TrimSpace(overrideDLLPath)

	searchOrder := make([]string, 0, len(vecowDLLCandidates)+3)
	if overridePath != "" {
		searchOrder = append(searchOrder, overridePath)
	}
	if customDLL != "" {
		searchOrder = append(searchOrder, customDLL)
	}
	for _, dllName := range vecowDLLCandidates {
		if driverDir != "" {
			searchOrder = append(searchOrder, filepath.Join(driverDir, dllName))
			continue
		}
		searchOrder = append(searchOrder, dllName)
	}
	return searchOrder
}

func tryOpenWindowsAdapter(dllName string) (*windowsAdapter, windowsDriverProbeAttempt, error) {
	dllPathForLog := resolveDLLPathForLog(dllName)
	probeAttempt := windowsDriverProbeAttempt{
		dllPath: dllPathForLog,
		steps:   make([]string, 0, 10),
	}

	dll := windows.NewLazyDLL(dllName)
	if err := dll.Load(); err != nil {
		probeAttempt.steps = append(probeAttempt.steps, fmt.Sprintf("Load DLL: FAIL (%v)", err))
		probeAttempt.steps = append(probeAttempt.steps, "GPIO probe: skipped because DLL was not loaded")
		probeAttempt.outcome = "FAIL"
		return nil, probeAttempt, fmt.Errorf("load %s: %w", dllName, err)
	}
	probeAttempt.steps = append(probeAttempt.steps, "Load DLL: OK")

	adapter := &windowsAdapter{
		dllName: dllName,
		dll:     dll,
	}

	probeTests := []windowsGPIOProbeTest{
		{name: "ECX1K GPIO API", initProcName: "Initial", getProcName: "GetGPIO", setProcName: "SetGPIO", configProcName: "SetGPIOConfig", isConfigOptional: false},
		{name: "ECX1K DIO API", initProcName: "Initial", getProcName: "GetDIO1", setProcName: "SetDIO1", configProcName: "SetGPIOConfig", isConfigOptional: true},
		{name: "Legacy SIO GPIO API", initProcName: "initial_SIO", getProcName: "get_GPIO1", setProcName: "set_GPIO1", configProcName: "set_GPIO_config", isConfigOptional: true},
		{name: "Mixed compatibility API", initProcName: "Initial", getProcName: "GetDIO1", setProcName: "SetGPIO", configProcName: "", isConfigOptional: true},
	}

	probeAttempt.steps = append(probeAttempt.steps, fmt.Sprintf("Probe tests planned: %d", len(probeTests)))
	var lastProbeErr error
	for testIndex, probeTest := range probeTests {
		probeAttempt.steps = append(probeAttempt.steps, fmt.Sprintf("Test #%d (%s): START", testIndex+1, probeTest.name))

		testAdapter, gpioState, testErr := runWindowsGPIOProbeTest(dll, adapter, probeTest, &probeAttempt.steps)
		if testErr != nil {
			lastProbeErr = testErr
			probeAttempt.steps = append(probeAttempt.steps, fmt.Sprintf("Test #%d (%s): FAIL", testIndex+1, probeTest.name))
			continue
		}

		adapter.procInitial = testAdapter.procInitial
		adapter.procConfig = testAdapter.procConfig
		adapter.procGetGPIO = testAdapter.procGetGPIO
		adapter.procSetGPIO = testAdapter.procSetGPIO
		adapter.outputMask.Store(0)

		probeAttempt.steps = append(probeAttempt.steps, fmt.Sprintf("Test #%d (%s): PASS", testIndex+1, probeTest.name))
		probeAttempt.steps = append(probeAttempt.steps, fmt.Sprintf("GPIO probe: OK (state=0x%04X)", gpioState))
		probeAttempt.steps = append(probeAttempt.steps, "Unload DLL: skipped (DLL is active)")
		probeAttempt.outcome = "SUCCESS"
		return adapter, probeAttempt, nil
	}

	probeAttempt.steps = append(probeAttempt.steps, "GPIO probe: all tests failed")
	logDLLReleaseResult(adapter.dll, &probeAttempt.steps)
	probeAttempt.outcome = "FAIL"
	if lastProbeErr == nil {
		lastProbeErr = fmt.Errorf("no probe tests were executed")
	}
	return nil, probeAttempt, fmt.Errorf("GPIO ports unavailable in %s: %w", dllName, lastProbeErr)
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

func runWindowsGPIOProbeTest(dll *windows.LazyDLL, baseAdapter *windowsAdapter, probeTest windowsGPIOProbeTest, probeSteps *[]string) (*windowsAdapter, uint16, error) {
	testAdapter := &windowsAdapter{
		dllName: baseAdapter.dllName,
		dll:     baseAdapter.dll,
	}

	initialProc, initialErr := findRequiredProc(dll, probeTest.initProcName)
	if initialErr != nil {
		*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve init API %q: FAIL (%v)", probeTest.initProcName, initialErr))
		return nil, 0, initialErr
	}
	testAdapter.procInitial = initialProc
	*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve init API %q: OK", probeTest.initProcName))

	getProc, getErr := findRequiredProc(dll, probeTest.getProcName)
	if getErr != nil {
		*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve read API %q: FAIL (%v)", probeTest.getProcName, getErr))
		return nil, 0, getErr
	}
	testAdapter.procGetGPIO = getProc
	*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve read API %q: OK", probeTest.getProcName))

	setProc, setErr := findRequiredProc(dll, probeTest.setProcName)
	if setErr != nil {
		*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve write API %q: FAIL (%v)", probeTest.setProcName, setErr))
		return nil, 0, setErr
	}
	testAdapter.procSetGPIO = setProc
	*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve write API %q: OK", probeTest.setProcName))

	if probeTest.configProcName != "" {
		configProc, configFound, configErr := findOptionalProc(dll, probeTest.configProcName)
		if configFound {
			testAdapter.procConfig = configProc
			*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve config API %q: OK", probeTest.configProcName))
		} else if !probeTest.isConfigOptional {
			*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve config API %q: FAIL (%v)", probeTest.configProcName, configErr))
			return nil, 0, configErr
		} else {
			*probeSteps = append(*probeSteps, fmt.Sprintf("  Resolve config API %q: SKIP", probeTest.configProcName))
		}
	}

	if initErr := testAdapter.callInitial(); initErr != nil {
		*probeSteps = append(*probeSteps, fmt.Sprintf("  Initial call: FAIL (%v)", initErr))
		return nil, 0, initErr
	}
	*probeSteps = append(*probeSteps, "  Initial call: OK")

	if testAdapter.procConfig != nil {
		if configErr := testAdapter.callSetGPIOConfig(vecowGPIOConfigMask); configErr != nil {
			*probeSteps = append(*probeSteps, fmt.Sprintf("  GPIO config call: FAIL (%v)", configErr))
			return nil, 0, configErr
		}
		*probeSteps = append(*probeSteps, "  GPIO config call: OK")
	}

	if writeErr := testAdapter.callSetGPIO(0); writeErr != nil {
		*probeSteps = append(*probeSteps, fmt.Sprintf("  SetGPIO call: FAIL (%v)", writeErr))
		return nil, 0, writeErr
	}
	*probeSteps = append(*probeSteps, "  SetGPIO call: OK")

	var gpioState uint16
	if readErr := testAdapter.callGetGPIO(&gpioState); readErr != nil {
		*probeSteps = append(*probeSteps, fmt.Sprintf("  GPIO read call: FAIL (%v)", readErr))
		return nil, 0, readErr
	}
	*probeSteps = append(*probeSteps, "  GPIO read call: OK")
	return testAdapter, gpioState, nil
}

func findRequiredProc(dll *windows.LazyDLL, procName string) (*windows.LazyProc, error) {
	proc := dll.NewProc(procName)
	if err := proc.Find(); err != nil {
		return nil, err
	}
	return proc, nil
}

func findOptionalProc(dll *windows.LazyDLL, procName string) (*windows.LazyProc, bool, error) {
	proc := dll.NewProc(procName)
	if err := proc.Find(); err != nil {
		return nil, false, err
	}
	return proc, true, nil
}

func logDLLReleaseResult(dll *windows.LazyDLL, probeEvents *[]string) {
	if err := releaseLazyDLL(dll); err != nil {
		*probeEvents = append(*probeEvents, fmt.Sprintf("Unload DLL: FAIL (%v)", err))
		return
	}
	*probeEvents = append(*probeEvents, "Unload DLL: OK")
}

func formatWindowsDriverProbeLog(allProbeAttempts []windowsDriverProbeAttempt) string {
	if len(allProbeAttempts) == 0 {
		return "Windows DLL probe log is empty."
	}

	formattedLines := make([]string, 0, len(allProbeAttempts)*8+4)
	formattedLines = append(formattedLines, "Windows DLL probe report")
	formattedLines = append(formattedLines, "========================")
	for probeIndex, probeAttempt := range allProbeAttempts {
		formattedLines = append(formattedLines, "")
		formattedLines = append(formattedLines, fmt.Sprintf("DLL #%d", probeIndex+1))
		formattedLines = append(formattedLines, fmt.Sprintf("Path: %s", probeAttempt.dllPath))
		for _, step := range probeAttempt.steps {
			formattedLines = append(formattedLines, fmt.Sprintf("  - %s", step))
		}
		formattedLines = append(formattedLines, fmt.Sprintf("Result: %s", probeAttempt.outcome))
	}
	return strings.Join(formattedLines, "\n")
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
