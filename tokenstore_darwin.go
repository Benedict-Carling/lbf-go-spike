//go:build cgo

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
)

// The Keychain API only lets an executable with the creator's file name delete an item; Apple's security tool can delete any.
func deleteTokens(name string) (bool, error) {
	out, err := exec.Command("/usr/bin/security", "delete-generic-password", "-s", name, "-a", "MSALCache").CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return true, nil
}
