package main

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func deleteTokens(name string) (bool, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return false, err
	}
	err = os.Remove(filepath.Join(dir, ".IdentityService", name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
