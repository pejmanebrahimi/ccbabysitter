package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PastSession is a conversation Claude Code kept on disk: enough to find it
// and pick it up again, and nothing of what was said in it.
type PastSession struct {
	ID           string
	Name         string
	Cwd          string
	LastActivity time.Time
}

// pastEdgeBytes is how much of each end of a transcript is read for its
// folder and its name. A long conversation runs to many megabytes and only
// two short fields of it are wanted.
const pastEdgeBytes = 64 << 10

// pastRecord is the subset of a transcript line ListPast reads.
type pastRecord struct {
	titleRecord
	Type string `json:"type"`
	Cwd  string `json:"cwd"`
}

// ListPast finds the transcripts directly inside the project folders under
// projectsDir that were written to at or after since and hold a message,
// leaves out the ids in skip, and returns at most max of them, newest first. A session whose
// transcript sits in two project folders is listed once, by its newest.
func ListPast(projectsDir string, since time.Time, max int, skip map[string]bool) []PastSession {
	type found struct {
		id, path string
		mod      time.Time
	}
	best := map[string]found{}
	dirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		folder := filepath.Join(projectsDir, d.Name())
		files, err := os.ReadDir(folder)
		if err != nil {
			continue
		}
		for _, fe := range files {
			name := fe.Name()
			if fe.IsDir() || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			if !ValidID(id) || skip[id] {
				continue
			}
			info, err := fe.Info()
			if err != nil || info.ModTime().Before(since) {
				continue
			}
			if prev, seen := best[id]; seen && !info.ModTime().After(prev.mod) {
				continue
			}
			best[id] = found{id: id, path: filepath.Join(folder, name), mod: info.ModTime()}
		}
	}
	list := make([]found, 0, len(best))
	for _, f := range best {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].mod.Equal(list[j].mod) {
			return list[i].mod.After(list[j].mod)
		}
		return list[i].id < list[j].id
	})
	out := make([]PastSession, 0, max)
	for _, f := range list {
		if len(out) == max {
			break
		}
		// A transcript with no message in it, such as an empty file, has
		// nothing to resume, so it is left out.
		cwd, name, said := readPastEdges(f.path)
		if !said {
			continue
		}
		out = append(out, PastSession{ID: f.id, Name: name, Cwd: cwd, LastActivity: f.mod})
	}
	return out
}

// readPastEdges reads a transcript's folder from its first lines and its
// name by the same rules as a live session's, from the sidecar title file
// or from the title records within either end of the file. A line cut in
// half at the edge of what was read is not valid JSON and is skipped like
// any other that is not. said reports whether either end holds a message,
// from the person or from the model.
func readPastEdges(path string) (cwd, name string, said bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	head := make([]byte, pastEdgeBytes)
	n, _ := io.ReadFull(f, head)
	var titles titleTracker
	scan := func(chunk []byte) {
		for _, line := range bytes.Split(chunk, []byte("\n")) {
			var r pastRecord
			if json.Unmarshal(bytes.TrimSpace(line), &r) != nil {
				continue
			}
			if cwd == "" && r.Cwd != "" {
				cwd = r.Cwd
			}
			if r.Type == "user" || r.Type == "assistant" {
				said = true
			}
			titles.record(r.Type, &r.titleRecord)
		}
	}
	scan(head[:n])
	if info, err := f.Stat(); err == nil && info.Size() > int64(n) {
		// The record after a custom-title that ends the head window is
		// somewhere unread, so the tail cannot say whether it was the
		// automatic name. It is dropped rather than taken.
		titles.pending, titles.hasPending = "", false
		from := info.Size() - pastEdgeBytes
		if from < int64(n) {
			from = int64(n)
		}
		tail := make([]byte, info.Size()-from)
		m, _ := f.ReadAt(tail, from)
		scan(tail[:m])
	}
	// Nothing more is written to a conversation that is not running, so a
	// name at the very end is final.
	titles.settle()
	name = sidecarTitle(path)
	if name == "" {
		name = titles.title()
	}
	return capBytes(cwd, maxCwdBytes), name, said
}
