package main

import (
	"slices"
	"testing"
)

func TestWindowsArgs(t *testing.T) {
	for _, tc := range []struct {
		name, commandLine string
		want              []string
	}{
		{
			"PowerShell 5.1, trailing backslash then a flag",
			`C:\lbf\lbf.exe publish ".\test data\" --dry-run  --tag role=x`,
			[]string{"publish", `.\test data\`, "--dry-run", "--tag", "role=x"},
		},
		{
			"PowerShell 5.1, trailing backslash last",
			`C:\lbf\lbf.exe fetch 20261001-x-y-0000 --out "D:\my data\"`,
			[]string{"fetch", "20261001-x-y-0000", "--out", `D:\my data\`},
		},
		{
			"PowerShell 5.1, trailing backslash then a quoted path",
			`C:\lbf\lbf.exe publish "C:\test data\plate 1\" --properties "C:\pr1\meta data\props.json" --dry-run --json`,
			[]string{"publish", `C:\test data\plate 1\`, "--properties", `C:\pr1\meta data\props.json`, "--dry-run", "--json"},
		},
		{
			"PowerShell 5.1, trailing backslash then quoted values with spaces",
			`C:\lbf\lbf.exe publish "C:\test data\plate 1\" --name "Plate P-0001 read" --property "operator=A. Researcher"`,
			[]string{"publish", `C:\test data\plate 1\`, "--name", "Plate P-0001 read", "--property", "operator=A. Researcher"},
		},
		{
			"escaped as cmd, Go and PowerShell 7.3 do",
			`"C:\Program Files\lbf\lbf.exe" publish "C:\a b\\" --name "Run 1" --out D:\out`,
			[]string{"publish", `C:\a b\`, "--name", "Run 1", "--out", `D:\out`},
		},
		{
			"an escaped quote inside a value",
			`lbf.exe publish data --description "a \"quoted\"word"`,
			[]string{"publish", "data", "--description", `a "quoted"word`},
		},
	} {
		if got := windowsArgs(tc.commandLine); !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}
