//go:build !windows

package main

import "os"

func enableVT(*os.File) (restore func(), ok bool) {
	return func() {}, true
}
