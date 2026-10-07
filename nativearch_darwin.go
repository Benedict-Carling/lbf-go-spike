package main

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// nativeArch sees through Rosetta, which reports amd64 to the process on Apple Silicon.
func nativeArch() string {
	if t, err := unix.SysctlUint32("sysctl.proc_translated"); err == nil && t == 1 {
		return "arm64"
	}
	return runtime.GOARCH
}
