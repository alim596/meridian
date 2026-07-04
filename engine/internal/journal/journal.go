// Package journal persists the sequenced event stream as append-only JSONL,
// one file per instrument. It is the exchange's source of truth: the whole
// session can be reconstructed (and audited) from the journal alone — see
// cmd/replay.
package journal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/alim596/meridian/internal/engine"
)

const flushInterval = 500 * time.Millisecond

type Journal struct {
	dir     string
	mu      sync.Mutex
	writers map[string]*bufio.Writer
	files   map[string]*os.File
	ch      chan engine.Event
}

func New(dir string) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("journal dir: %w", err)
	}
	return &Journal{
		dir:     dir,
		writers: make(map[string]*bufio.Writer),
		files:   make(map[string]*os.File),
		ch:      make(chan engine.Event, 8192),
	}, nil
}

// OnEvent enqueues an event for persistence; called from the bus dispatcher.
func (j *Journal) OnEvent(ev engine.Event) { j.ch <- ev }

// Run drains the queue to disk with periodic flushes. Call Close after the
// context is done to flush remaining buffered events.
func (j *Journal) Run(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-j.ch:
			j.write(ev)
		case <-ticker.C:
			j.flush()
		}
	}
}

func (j *Journal) write(ev engine.Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	w := j.writers[ev.Instrument]
	if w == nil {
		path := filepath.Join(j.dir, fmt.Sprintf("journal-%s.jsonl", ev.Instrument))
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "journal: open %s: %v\n", path, err)
			return
		}
		j.files[ev.Instrument] = f
		w = bufio.NewWriterSize(f, 64*1024)
		j.writers[ev.Instrument] = w
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	w.Write(b)
	w.WriteByte('\n')
}

func (j *Journal) flush() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, w := range j.writers {
		w.Flush()
	}
}

// Close drains pending events and flushes all files.
func (j *Journal) Close() {
	for {
		select {
		case ev := <-j.ch:
			j.write(ev)
		default:
			j.flush()
			j.mu.Lock()
			for _, f := range j.files {
				f.Close()
			}
			j.mu.Unlock()
			return
		}
	}
}

// Read streams a journal file back as events, in order.
func Read(path string, fn func(engine.Event) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		var ev engine.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	return sc.Err()
}
