//go:build windows || (darwin && cgo)

package main

import (
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity/cache"
)

var tokenCache = sync.OnceValues(func() (azidentity.Cache, bool) {
	c, err := cache.New(&cache.Options{Name: tokenCacheName})
	return c, err == nil
})

// azidentity/cache keeps CAE tokens apart under name+".cae" and exposes no way to delete either.
func clearTokenCache(name string) (bool, error) {
	found := false
	for _, n := range []string{name, name + ".cae"} {
		ok, err := deleteTokens(n)
		if err != nil {
			return found, err
		}
		found = found || ok
	}
	return found, nil
}
