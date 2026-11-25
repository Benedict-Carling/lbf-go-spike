package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func enableVT(f *os.File) (restore func(), ok bool) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return nil, false
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return nil, false
	}
	return func() { windows.SetConsoleMode(h, mode) }, true
}
