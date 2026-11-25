package main

import (
	"os"
	"slices"
	"testing"
)

func TestCommandArgsMatchGoWithoutPowerShellQuirks(t *testing.T) {
	if got := commandArgs(); !slices.Equal(got, os.Args[1:]) {
		t.Fatalf("got %q, os.Args has %q", got, os.Args[1:])
	}
}
