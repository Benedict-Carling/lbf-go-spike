//go:build windows || (darwin && cgo)

package main

import "github.com/Azure/azure-sdk-for-go/sdk/azidentity"

func hermeticTokenCache() (azidentity.Cache, error) {
	return azidentity.Cache{}, nil
}
