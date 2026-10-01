//go:build windows || (darwin && cgo)

package main

import (
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity/cache"
)

var tokenCache = sync.OnceValues(func() (azidentity.Cache, bool) {
	c, err := cache.New(&cache.Options{Name: "lbf"})
	return c, err == nil
})
