package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Entry is one line of the activity feed and the rolling log.
type Entry struct {
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Session   string    `json:"session,omitempty"`
	Automatic bool      `json:"automatic"`
	Reason    string    `json:"reason,omitempty"`
	Message   string    `json:"message"`
}

// Log appends to log.txt (rolling at maxBytes, keeping the newest half) and
// keeps the last 500 entries in memory as the activity feed. Subscribers
// receive every entry written after they subscribe.
type Log struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
	ring     []Entry
	subs     map[int]chan Entry
	nextSub  int
}

// NewLog opens (creating if needed) log.txt inside dir.
func NewLog(dir string) (*Log, error) {
	if err := EnsureDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "log.txt")
	// A log left behind by an earlier build may be readable by other
	// accounts on the machine; it holds session names and working
	// directories, so it is tightened here rather than only on creation.
	_ = os.Chmod(path, fileMode)
	return &Log{path: path, maxBytes: 1 << 20, subs: map[int]chan Entry{}}, nil
}

// Info records a routine message.
func (l *Log) Info(session, msg string) {
	l.write(Entry{Level: "INFO", Session: session, Message: msg})
}

// Debug records a line for whoever reads log.txt to find out what
// happened, and nothing a person using the page can act on: Activity and
// its subscribers never see it.
func (l *Log) Debug(session, msg string) {
	l.write(Entry{Level: "DEBUG", Session: session, Message: msg})
}

// Error records a failure.
func (l *Log) Error(session, msg string) {
	l.write(Entry{Level: "ERROR", Session: session, Message: msg})
}

// Auto records an action CC Babysitter took on its own, with the reason it took it.
func (l *Log) Auto(session, reason, msg string) {
	l.write(Entry{Level: "INFO", Session: session, Automatic: true, Reason: reason, Message: msg})
}

func (l *Log) write(e Entry) {
	e.Time = time.Now()
	line := fmt.Sprintf("%s %-5s %s%s%s", e.Time.Format("2006-01-02 15:04:05"), e.Level, tag(e), e.Message, reason(e))

	l.mu.Lock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err == nil {
		_, _ = f.WriteString(strings.ReplaceAll(line, "\n", " | ") + "\n")
		_ = f.Close()
		if fi, err := os.Stat(l.path); err == nil && fi.Size() > l.maxBytes {
			if data, err := os.ReadFile(l.path); err == nil {
				lines := strings.Split(string(data), "\n")
				_ = os.WriteFile(l.path, []byte(strings.Join(lines[len(lines)/2:], "\n")), fileMode)
			}
		}
	}
	if e.Level == "DEBUG" {
		l.mu.Unlock()
		return
	}
	l.ring = append(l.ring, e)
	if len(l.ring) > 500 {
		l.ring = l.ring[len(l.ring)-500:]
	}
	subs := make([]chan Entry, 0, len(l.subs))
	for _, c := range l.subs {
		subs = append(subs, c)
	}
	l.mu.Unlock()

	for _, c := range subs {
		select {
		case c <- e:
		default:
		}
	}
}

func tag(e Entry) string {
	if e.Session == "" {
		return ""
	}
	return e.Session + ": "
}

func reason(e Entry) string {
	if !e.Automatic {
		return ""
	}
	return " [automatic: " + e.Reason + "]"
}

// Recent returns up to n newest entries, oldest first, optionally filtered by session.
func (l *Log) Recent(n int, session string) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Entry
	for i := len(l.ring) - 1; i >= 0 && len(out) < n; i-- {
		if session == "" || l.ring[i].Session == session {
			out = append(out, l.ring[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Subscribe returns a channel that receives every entry written from now on,
// and a cancel function to stop receiving. The writer never blocks on a slow
// or abandoned subscriber.
func (l *Log) Subscribe() (<-chan Entry, func()) {
	l.mu.Lock()
	id := l.nextSub
	l.nextSub++
	c := make(chan Entry, 64)
	l.subs[id] = c
	l.mu.Unlock()
	return c, func() { l.mu.Lock(); delete(l.subs, id); l.mu.Unlock() }
}
