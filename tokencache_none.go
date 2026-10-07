//go:build !(windows || (darwin && cgo))

package main

import "github.com/Azure/azure-sdk-for-go/sdk/azidentity"

// The OS credential stores on Linux and macOS need cgo; static builds sign in per run or use az.
func tokenCache() (azidentity.Cache, bool) {
	return azidentity.Cache{}, false
}

func clearTokenCache(string) (bool, error) {
	return false, nil
}
