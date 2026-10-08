package main

import "strings"

// The arguments after the program's name in a Windows command line.
func windowsArgs(commandLine string) []string {
	args := splitCommandLine(commandLine)
	if len(args) == 0 {
		return nil
	}
	return args[1:]
}

// Splits a Windows command line as Go's os package does for os.Args, but for a value PowerShell 5.1 quoted that ends in a backslash.
func splitCommandLine(cmd string) []string {
	var args []string
	for len(cmd) > 0 {
		if cmd[0] == ' ' || cmd[0] == '\t' {
			cmd = cmd[1:]
			continue
		}
		var arg string
		arg, cmd = nextArg(cmd)
		args = append(args, arg)
	}
	return args
}

func nextArg(cmd string) (arg, rest string) {
	var b strings.Builder
	var inquote bool
	var nslash int
	for ; len(cmd) > 0; cmd = cmd[1:] {
		c := cmd[0]
		switch c {
		case ' ', '\t':
			if !inquote {
				b.WriteString(strings.Repeat(`\`, nslash))
				return b.String(), cmd[1:]
			}
		case '"':
			// PowerShell 5.1 quotes 'C:\My Folder\' as "C:\My Folder\" without doubling the backslash.
			if inquote && nslash%2 == 1 && (len(cmd) == 1 || cmd[1] == ' ' || cmd[1] == '\t') {
				b.WriteString(strings.Repeat(`\`, nslash))
				inquote, nslash = false, 0
				continue
			}
			b.WriteString(strings.Repeat(`\`, nslash/2))
			if nslash%2 == 0 {
				if inquote && len(cmd) > 1 && cmd[1] == '"' {
					b.WriteByte(c)
					cmd = cmd[1:]
				}
				inquote = !inquote
			} else {
				b.WriteByte(c)
			}
			nslash = 0
			continue
		case '\\':
			nslash++
			continue
		}
		b.WriteString(strings.Repeat(`\`, nslash))
		nslash = 0
		b.WriteByte(c)
	}
	b.WriteString(strings.Repeat(`\`, nslash))
	return b.String(), ""
}
