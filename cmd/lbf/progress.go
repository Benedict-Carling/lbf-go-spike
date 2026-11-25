package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	redrawEvery  = 100 * time.Millisecond
	reportEvery  = 30 * time.Second
	rateWindow   = 5 * time.Second
	maxBarWidth  = 20
	minNameWidth = 12
)

// Redraws in place on a terminal; elsewhere (logs, HPC jobs) prints a line per file and a periodic summary.
// Only loop and close write, so a stalled console (e.g. Windows QuickEdit selection) never blocks a transfer.
type progress struct {
	out   io.Writer
	live  bool
	size  func() (width, height int)
	files int
	total int64

	mu         sync.Mutex
	done       int
	settled    int64
	active     []*fileBar
	pending    []string
	samples    []sample
	frame      []int
	lastReport time.Time
	stop       chan struct{}
	stopped    chan struct{}
}

type sample struct {
	at time.Time
	n  int64
}

type fileBar struct {
	name string
	size int64
	n    atomic.Int64
}

func (b *fileBar) set(n int64) { b.n.Store(n) }

func (b *fileBar) transferred() int64 { return min(max(b.n.Load(), 0), b.size) }

func newProgress(out io.Writer, files int, total int64) *progress {
	now := time.Now()
	p := &progress{
		out:        out,
		live:       isLive(out),
		size:       func() (int, int) { return termSize(out) },
		files:      files,
		total:      total,
		samples:    []sample{{now, 0}},
		lastReport: now,
		stop:       make(chan struct{}),
		stopped:    make(chan struct{}),
	}
	go p.loop()
	return p
}

func (p *progress) loop() {
	defer close(p.stopped)
	t := time.NewTicker(redrawEvery)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case now := <-t.C:
			io.WriteString(p.out, p.render(now, false))
		}
	}
}

func (p *progress) start(name string, size int64) *fileBar {
	b := &fileBar{name: name, size: size}
	p.mu.Lock()
	p.active = append(p.active, b)
	p.mu.Unlock()
	return b
}

func (p *progress) end(b *fileBar, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, a := range p.active {
		if a == b {
			p.active = append(p.active[:i], p.active[i+1:]...)
			break
		}
	}
	if ok {
		p.done++
		p.settled += b.size
		p.pending = append(p.pending, fmt.Sprintf("  [%d/%d] %s (%s)", p.done, p.files, printable(b.name), humanBytes(b.size)))
	}
}

func (p *progress) close() {
	close(p.stop)
	<-p.stopped
	io.WriteString(p.out, p.render(time.Now(), true))
}

func (p *progress) render(now time.Time, final bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.live {
		s := strings.Join(append(p.pending, ""), "\n")
		p.pending = nil
		if !final && now.Sub(p.lastReport) >= reportEvery {
			p.lastReport = now
			s += fmt.Sprintf("  ... %d/%d files, %s\n", p.done, p.files, p.summary(now))
		}
		return s
	}

	width, height := p.size()
	var b strings.Builder
	// Rows the last frame now occupies, should a narrower window have rewrapped it.
	up := 0
	for _, w := range p.frame {
		up += max(1, (w+width-1)/width)
	}
	if up > 0 {
		fmt.Fprintf(&b, "\x1b[%dA\r", up)
	}
	for _, line := range p.pending {
		b.WriteString(line + "\x1b[K\n")
	}
	p.pending = nil
	p.frame = p.frame[:0]
	if final {
		b.WriteString("\x1b[J")
		return b.String()
	}

	// One column short of the edge, where some consoles wrap early.
	width--
	shown := p.active[:min(len(p.active), max(height-3, 0))]
	barWidth := width - 2 - len(counts(0, 0)) - 2 - minNameWidth - 3
	left := func(n, size int64) string {
		s := "  "
		if fb := bar(n, size, barWidth); fb != "" {
			s += fb + " "
		}
		return s + counts(n, size) + "  "
	}
	line := func(s string) {
		s = fit(s, width)
		b.WriteString(s + "\x1b[K\n")
		p.frame = append(p.frame, len(s))
	}
	for _, f := range shown {
		l := left(f.transferred(), f.size)
		line(l + clipLeft(printable(f.name), width-len(l)))
	}

	n := p.transferred()
	l := left(n, p.total)
	parts := []string{fmt.Sprintf("%d/%d files", p.done, p.files)}
	if r := p.rate(now, n); r > 0 {
		parts = append(parts, "ETA "+shortDuration(time.Duration(float64(p.total-n)/r*float64(time.Second))), humanBytes(int64(r))+"/s")
	}
	if hidden := len(p.active) - len(shown); hidden > 0 {
		parts = append(parts, fmt.Sprintf("(+%d more)", hidden))
	}
	trailer := parts[0]
	for _, part := range parts[1:] {
		if len(l)+len(trailer)+2+len(part) > width {
			break
		}
		trailer += "  " + part
	}
	line(l + trailer)
	b.WriteString("\x1b[J")
	return b.String()
}

func (p *progress) transferred() int64 {
	n := p.settled
	for _, b := range p.active {
		n += b.transferred()
	}
	return n
}

func (p *progress) rate(now time.Time, n int64) float64 {
	p.samples = append(p.samples, sample{now, n})
	for len(p.samples) > 2 && now.Sub(p.samples[1].at) >= rateWindow {
		p.samples = p.samples[1:]
	}
	first := p.samples[0]
	if d := now.Sub(first.at); d > 0 && n > first.n {
		return float64(n-first.n) / d.Seconds()
	}
	return 0
}

func (p *progress) summary(now time.Time) string {
	n := p.transferred()
	s := fmt.Sprintf("%s / %s", humanBytes(n), humanBytes(p.total))
	if r := p.rate(now, n); r > 0 {
		s += fmt.Sprintf(", %s/s, ETA %s", humanBytes(int64(r)), shortDuration(time.Duration(float64(p.total-n)/r*float64(time.Second))))
	}
	return s
}

func counts(n, total int64) string {
	return fmt.Sprintf("%3d%%  %10s / %-10s", percent(n, total), humanBytes(n), humanBytes(total))
}

func bar(n, total int64, width int) string {
	width = min(width, maxBarWidth)
	if width < 4 {
		return ""
	}
	filled := width
	if total > 0 {
		filled = int(float64(width) * float64(n) / float64(total))
	}
	s := strings.Repeat("=", filled)
	if filled < width {
		s += ">" + strings.Repeat(" ", width-filled-1)
	}
	return "[" + s + "]"
}

func percent(n, total int64) int {
	if total <= 0 {
		return 100
	}
	return int(100 * n / total)
}

// Bidi overrides and other invisible characters could reorder or hide parts of a name.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		return r
	}, s)
}

// No character is wider on screen than its UTF-8 length, so byte counts never underestimate a line's width.
func fit(s string, w int) string {
	if len(s) <= w {
		return s
	}
	w = max(w, 0)
	for w > 0 && !utf8.RuneStart(s[w]) {
		w--
	}
	return s[:w]
}

func clipLeft(s string, w int) string {
	if len(s) <= w {
		return s
	}
	if w < 4 {
		return fit("...", w)
	}
	i := len(s) - (w - 3)
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return "..." + s[i:]
}

func shortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func isLive(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) || os.Getenv("TERM") == "dumb" {
		return false
	}
	_, ok = enableVT(f)
	return ok
}

func termSize(w io.Writer) (int, int) {
	if f, ok := w.(*os.File); ok {
		if width, height, err := term.GetSize(int(f.Fd())); err == nil && width > 0 && height > 0 {
			return width, height
		}
	}
	return 80, 24
}

var (
	stderrLive  = sync.OnceValue(func() bool { return isLive(os.Stderr) })
	statusMu    sync.Mutex
	statusShown bool
)

// status shows msg on the terminal's last line until replaced, cleared with "", or overwritten by a logf.
func status(msg string) {
	if !stderrLive() {
		return
	}
	statusMu.Lock()
	defer statusMu.Unlock()
	fmt.Fprint(os.Stderr, "\r\x1b[K")
	statusShown = msg != ""
	if statusShown {
		width, _ := termSize(os.Stderr)
		fmt.Fprint(os.Stderr, fit(printable(msg)+"...", width-1))
	}
}

func logf(format string, a ...any) {
	statusMu.Lock()
	defer statusMu.Unlock()
	if statusShown {
		fmt.Fprint(os.Stderr, "\r\x1b[K")
		statusShown = false
	}
	fmt.Fprintf(os.Stderr, format, a...)
}
