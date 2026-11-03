package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	progressBarWidth = 30
	clearLine        = "\r\x1b[K"
)

// progressBar draws the number of finished items on a single, transient line.
// When disabled it draws nothing, but messages are still printed. All methods
// are safe for concurrent use.
type progressBar struct {
	mu      sync.Mutex
	w       io.Writer
	enabled bool
	done    int
	total   int
}

func newProgressBar(w io.Writer, enabled bool) *progressBar {
	return &progressBar{w: w, enabled: enabled}
}

func (p *progressBar) start(total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total = total
	p.draw()
}

func (p *progressBar) advance() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done++
	p.draw()
}

// printf prints a message above the bar, so that the two do not get mixed up.
func (p *progressBar) printf(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear()
	_, _ = fmt.Fprintf(p.w, format, args...)
	p.draw()
}

// finish removes the bar from the terminal.
func (p *progressBar) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear()
	p.total = 0
}

func (p *progressBar) visible() bool {
	return p.enabled && p.total > 0
}

func (p *progressBar) clear() {
	if p.visible() {
		_, _ = io.WriteString(p.w, clearLine)
	}
}

func (p *progressBar) draw() {
	if !p.visible() {
		return
	}
	filled := progressBarWidth * p.done / p.total
	bar := "[" + strings.Repeat("█", filled) + strings.Repeat("░", progressBarWidth-filled) + "]"
	_, _ = fmt.Fprintf(p.w, "\rresolving actions %s %d/%d", bar, p.done, p.total)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
