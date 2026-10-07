//go:build !windows && !darwin

package main

import "runtime"

func nativeArch() string { return runtime.GOARCH }
