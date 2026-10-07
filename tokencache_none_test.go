//go:build !(windows || (darwin && cgo))

package main

var hermeticTokenCache = tokenCache
