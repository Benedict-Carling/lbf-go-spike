package main

import "golang.org/x/sys/windows"

func commandArgs() []string {
	return windowsArgs(windows.UTF16PtrToString(windows.GetCommandLine()))
}
