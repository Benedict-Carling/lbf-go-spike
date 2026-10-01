package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

var errNoChoice = errors.New("no storage account chosen")

type picker func(prompt string, labels []string) (int, error)

// Returns nil when nobody is at a terminal to answer.
func terminalPicker() picker {
	in, out := os.Stdin, os.Stderr
	if term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd())) {
		return func(prompt string, labels []string) (int, error) {
			if restoreVT, ok := enableVT(out); ok {
				defer restoreVT()
				if state, err := term.MakeRaw(int(in.Fd())); err == nil {
					defer term.Restore(int(in.Fd()), state)
					return pickArrows(in, out, prompt, labels)
				}
			}
			return pickNumbered(in, out, prompt, labels)
		}
	}
	// Git Bash (mintty) gives programs pipes, not a console, so raw mode is unavailable but line input works.
	if isatty.IsCygwinTerminal(in.Fd()) && isatty.IsCygwinTerminal(out.Fd()) {
		return func(prompt string, labels []string) (int, error) {
			return pickNumbered(in, out, prompt, labels)
		}
	}
	return nil
}

func pickArrows(in io.Reader, out io.Writer, prompt string, labels []string) (int, error) {
	cur := 0
	draw := func() {
		for i, l := range labels {
			mark := "( )"
			if i == cur {
				mark = "(*)"
			}
			fmt.Fprintf(out, "\r\x1b[2K  %s %s\r\n", mark, l)
		}
	}
	fmt.Fprintf(out, "%s (arrow keys, Enter; q to cancel)\r\n", prompt)
	draw()

	const (
		plain = iota
		esc
		csi
	)
	state := plain
	buf := make([]byte, 64)
	for {
		n, err := in.Read(buf)
		for _, k := range buf[:n] {
			switch {
			case state == esc && (k == '[' || k == 'O'):
				state = csi
				continue
			case state == csi:
				state = plain
				switch k {
				case 'A':
					cur = max(cur-1, 0)
				case 'B':
					cur = min(cur+1, len(labels)-1)
				}
				continue
			}
			state = plain
			switch k {
			case '\r', '\n':
				return cur, nil
			case 3, 'q':
				return 0, errNoChoice
			case 0x1b:
				state = esc
			case 'k':
				cur = max(cur-1, 0)
			case 'j':
				cur = min(cur+1, len(labels)-1)
			}
		}
		if err != nil {
			return 0, errNoChoice
		}
		fmt.Fprintf(out, "\x1b[%dA", len(labels))
		draw()
	}
}

func pickNumbered(in io.Reader, out io.Writer, prompt string, labels []string) (int, error) {
	fmt.Fprintln(out, prompt)
	for i, l := range labels {
		fmt.Fprintf(out, "  %d) %s\n", i+1, l)
	}
	sc := bufio.NewScanner(in)
	for {
		fmt.Fprintf(out, "Choose [1-%d]: ", len(labels))
		if !sc.Scan() {
			fmt.Fprintln(out)
			return 0, errNoChoice
		}
		if n, err := strconv.Atoi(strings.TrimSpace(sc.Text())); err == nil && n >= 1 && n <= len(labels) {
			return n - 1, nil
		}
	}
}
