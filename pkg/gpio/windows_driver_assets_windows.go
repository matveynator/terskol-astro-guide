//go:build windows

package gpio

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func PrepareWindowsDriverDirectory(embeddedFiles fs.FS) (func(), error) {
	existingDriverDirectory := strings.TrimSpace(os.Getenv("CHICHA_GPIO_WINDOWS_DRIVER_DIR"))
	if existingDriverDirectory != "" {
		return func() {}, nil
	}

	tempDriverDirectory, err := os.MkdirTemp("", "chicha-gpio-driver-*")
	if err != nil {
		return nil, fmt.Errorf("create windows driver temp directory: %w", err)
	}

	driverFileNames := []string{
		"drv.dll",
		"WinRing0x64.dll",
		"OpenHardwareMonitorLib.dll",
		"drv64.sys",
		"Vecow.dll",
	}
	for _, driverFileName := range driverFileNames {
		embeddedPath := path.Join("static", "driver", driverFileName)
		driverFileBytes, readErr := fs.ReadFile(embeddedFiles, embeddedPath)
		if readErr != nil {
			_ = os.RemoveAll(tempDriverDirectory)
			return nil, fmt.Errorf("read embedded driver %s: %w", driverFileName, readErr)
		}

		destinationPath := filepath.Join(tempDriverDirectory, driverFileName)
		if writeErr := os.WriteFile(destinationPath, driverFileBytes, 0o755); writeErr != nil {
			_ = os.RemoveAll(tempDriverDirectory)
			return nil, fmt.Errorf("write embedded driver %s: %w", driverFileName, writeErr)
		}
	}

	if setEnvErr := os.Setenv("CHICHA_GPIO_WINDOWS_DRIVER_DIR", tempDriverDirectory); setEnvErr != nil {
		_ = os.RemoveAll(tempDriverDirectory)
		return nil, fmt.Errorf("set CHICHA_GPIO_WINDOWS_DRIVER_DIR: %w", setEnvErr)
	}

	currentPath := os.Getenv("PATH")
	pathListSeparator := string(os.PathListSeparator)
	if currentPath == "" {
		_ = os.Setenv("PATH", tempDriverDirectory)
	} else {
		_ = os.Setenv("PATH", strings.Join([]string{tempDriverDirectory, currentPath}, pathListSeparator))
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDriverDirectory)
	}
	return cleanup, nil
}
