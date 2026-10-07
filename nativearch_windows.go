package main

import (
	"debug/pe"
	"runtime"

	"golang.org/x/sys/windows"
)

// nativeArch sees through x64 emulation on Arm64 Windows, which reports amd64 to the process.
func nativeArch() string {
	var process, native uint16
	if windows.IsWow64Process2(windows.CurrentProcess(), &process, &native) == nil && native == pe.IMAGE_FILE_MACHINE_ARM64 {
		return "arm64"
	}
	return runtime.GOARCH
}
