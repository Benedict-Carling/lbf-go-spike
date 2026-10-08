//go:build !windows

package main

import "os"

func commandArgs() []string { return os.Args[1:] }
