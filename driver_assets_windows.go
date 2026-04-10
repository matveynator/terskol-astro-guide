//go:build windows

package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed static/driver/drv.dll static/driver/WinRing0x64.dll static/driver/OpenHardwareMonitorLib.dll static/driver/drv64.sys static/driver/Vecow.dll
var windowsDriverFiles embed.FS

func prepareWindowsDriverDirectory() (func(), error) {
	tempDriverDirectory, err := os.MkdirTemp("", "chicha-gpio-driver-*")
	if err != nil {
		return nil, fmt.Errorf("create windows driver temp directory: %w", err)
	}

	copiedFileNames := []string{
		"drv.dll",
		"WinRing0x64.dll",
		"OpenHardwareMonitorLib.dll",
		"drv64.sys",
		"Vecow.dll",
	}
	for _, fileName := range copiedFileNames {
		embeddedPath := filepath.ToSlash(filepath.Join("static", "driver", fileName))
		fileBytes, readErr := fs.ReadFile(windowsDriverFiles, embeddedPath)
		if readErr != nil {
			_ = os.RemoveAll(tempDriverDirectory)
			return nil, fmt.Errorf("read embedded driver %s: %w", fileName, readErr)
		}

		destinationPath := filepath.Join(tempDriverDirectory, fileName)
		if writeErr := os.WriteFile(destinationPath, fileBytes, 0o644); writeErr != nil {
			_ = os.RemoveAll(tempDriverDirectory)
			return nil, fmt.Errorf("write embedded driver %s: %w", fileName, writeErr)
		}
	}

	if setEnvErr := os.Setenv("CHICHA_GPIO_WINDOWS_DRIVER_DIR", tempDriverDirectory); setEnvErr != nil {
		_ = os.RemoveAll(tempDriverDirectory)
		return nil, fmt.Errorf("set CHICHA_GPIO_WINDOWS_DRIVER_DIR: %w", setEnvErr)
	}

	currentPath := os.Getenv("PATH")
	if currentPath == "" {
		_ = os.Setenv("PATH", tempDriverDirectory)
	} else {
		_ = os.Setenv("PATH", strings.Join([]string{tempDriverDirectory, currentPath}, ";"))
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDriverDirectory)
	}
	return cleanup, nil
}
