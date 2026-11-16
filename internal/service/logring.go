package service

import (
	"sync"
	"time"
)

type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Source  string    `json:"source"`
	Message string    `json:"message"`
}

type logRing struct {
	mu      sync.RWMutex
	entries []LogEntry
	size    int
}

func newLogRing(size int) *logRing {
	if size < 1 {
		size = 1
	}
	return &logRing{size: size}
}

func (l *logRing) add(entry LogEntry) {
	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == l.size {
		copy(l.entries, l.entries[1:])
		l.entries[len(l.entries)-1] = entry
		return
	}
	l.entries = append(l.entries, entry)
}

func (l *logRing) log(level, source, message string) {
	l.add(LogEntry{Level: level, Source: source, Message: message})
}

func (l *logRing) tail(limit int) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 || limit > len(l.entries) {
		limit = len(l.entries)
	}
	start := len(l.entries) - limit
	result := make([]LogEntry, limit)
	copy(result, l.entries[start:])
	return result
}

func (l *logRing) clear() {
	l.mu.Lock()
	l.entries = nil
	l.mu.Unlock()
}
