package main

import (
	"bufio"
	"context"
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
func terminalPicker(ctx context.Context) picker {
	in, out := os.Stdin, os.Stderr
	if term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd())) {
		return func(prompt string, labels []string) (int, error) {
			if os.Getenv("TERM") != "dumb" {
				if restoreVT, ok := enableVT(out); ok {
					defer restoreVT()
					if state, err := term.MakeRaw(int(in.Fd())); err == nil {
						defer term.Restore(int(in.Fd()), state)
						return pickArrows(ctx, in, out, prompt, labels)
					}
				}
			}
			return pickNumbered(ctx, in, out, prompt, labels)
		}
	}
	// Git Bash (mintty) gives programs pipes, not a console, so raw mode is unavailable but line input works.
	if isatty.IsCygwinTerminal(in.Fd()) && isatty.IsCygwinTerminal(out.Fd()) {
		return func(prompt string, labels []string) (int, error) {
			return pickNumbered(ctx, in, out, prompt, labels)
		}
	}
	return nil
}

// Reads only when asked, so a returned picker leaves no read pending; cancelling abandons the one in flight.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		b := make([]byte, len(p))
		n, err := c.r.Read(b)
		done <- result{b[:n], err}
	}()
	select {
	case r := <-done:
		return copy(p, r.b), r.err
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	}
}

func pickArrows(ctx context.Context, in io.Reader, out io.Writer, prompt string, labels []string) (int, error) {
	in = ctxReader{ctx, in}
	numbered := len(labels) <= 9
	cur, stray, drawn := 0, false, 0
	draw := func() {
		width, _ := termSize(out)
		if drawn > 0 {
			fmt.Fprintf(out, "\x1b[%dA", drawn)
		}
		line := func(s string) {
			fmt.Fprintf(out, "\r\x1b[2K%s\r\n", fit(s, width-1))
		}
		for i, l := range labels {
			mark := "( )"
			if i == cur {
				mark = "(*)"
			}
			if numbered {
				l = fmt.Sprintf("%d. %s", i+1, l)
			}
			line(fmt.Sprintf("  %s %s", mark, l))
		}
		drawn = len(labels)
		if stray {
			hint := "  Use the arrow keys and Enter"
			if numbered {
				hint += fmt.Sprintf(", or type its number (1-%d)", len(labels))
			}
			line(hint + "; q to cancel")
			drawn++
		}
		fmt.Fprint(out, "\x1b[J")
	}
	how := "arrow keys and Enter"
	if numbered {
		how += ", or its number"
	}
	fmt.Fprintf(out, "%s (%s; q to cancel)\r\n", prompt, how)
	draw()

	const (
		plain = iota
		esc
		csi
		ss3
	)
	state := plain
	move := func(k byte) {
		switch k {
		case 'A', 'k':
			cur, stray = max(cur-1, 0), false
		case 'B', 'j':
			cur, stray = min(cur+1, len(labels)-1), false
		case 'C', 'D':
		default:
			stray = true
		}
	}
	buf := make([]byte, 64)
	for {
		n, err := in.Read(buf)
		for _, k := range buf[:n] {
			switch state {
			case esc:
				state = plain
				switch k {
				case '[':
					state = csi
					continue
				case 'O':
					state = ss3
					continue
				}
			case csi:
				// Parameter and intermediate bytes, as in ESC [ 1 ; 5 A, come before the final byte.
				if k >= 0x20 && k <= 0x3f {
					continue
				}
				state = plain
				move(k)
				continue
			case ss3:
				state = plain
				move(k)
				continue
			}
			switch {
			case k == '\r' || k == '\n':
				if !stray {
					return cur, nil
				}
			case k == 3 || k == 'q':
				return 0, errNoChoice
			case k == 0x1b:
				state = esc
			case k == 'k' || k == 'j':
				move(k)
			case numbered && k >= '1' && int(k-'0') <= len(labels):
				return int(k - '1'), nil
			default:
				stray = true
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, errNoChoice
		}
		draw()
	}
}

func pickNumbered(ctx context.Context, in io.Reader, out io.Writer, prompt string, labels []string) (int, error) {
	fmt.Fprintln(out, prompt)
	for i, l := range labels {
		fmt.Fprintf(out, "  %d) %s\n", i+1, l)
	}
	sc := bufio.NewScanner(ctxReader{ctx, in})
	for {
		fmt.Fprintf(out, "Choose [1-%d]: ", len(labels))
		if !sc.Scan() {
			fmt.Fprintln(out)
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, errNoChoice
		}
		if n, err := strconv.Atoi(strings.TrimSpace(sc.Text())); err == nil && n >= 1 && n <= len(labels) {
			return n - 1, nil
		}
	}
}
