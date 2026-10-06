package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"
)

// Stats accumulates token and turn counts read from a transcript. It never
// holds any conversation content, only counts and metadata.
type Stats struct {
	Model            string    `json:"model"`
	Turns            int       `json:"turns"`
	LastActivity     time.Time `json:"lastActivity"`
	InputTokens      int64     `json:"inputTokens"`
	OutputTokens     int64     `json:"outputTokens"`
	CacheReadTokens  int64     `json:"cacheReadTokens"`
	CacheWriteTokens int64     `json:"cacheWriteTokens"`
	// Title is the name the session's own app shows for it: the sidecar
	// title file when there is one, else the latest name a person gave in
	// the transcript, else the latest AI title. It is empty when there is
	// none, and the page reads it through the name of the session or the
	// watch rather than here.
	Title string `json:"-"`
	// ScheduledTask reports whether the session is a run of a Claude Desktop
	// scheduled task: its first prompt starts with the <scheduled-task> tag
	// the app puts there. The views carry it outside Stats.
	ScheduledTask bool `json:"-"`
}

// StatsReader accumulates Stats over a transcript file, reading only the
// bytes appended since the previous call. A single API response is written
// as one record per content block, each repeating the same message id and
// usage, so the reader keeps the set of ids it has already counted to
// avoid multiplying token counts by the number of blocks in a response.
//
// A reader is tied to the file it last read. Handed a different path it
// starts over, since an offset and a set of message ids gathered from one
// transcript say nothing about another one.
type StatsReader struct {
	path   string
	offset int64
	stats  Stats
	seen   map[string]struct{}
	titles titleTracker
	// pendingRead is set when a read ended on a custom-title with nothing
	// after it yet. If the next read finds nothing new at all, it was not
	// the automatic name, whose agent-name line follows at once.
	pendingRead bool
	// prompted is set once the first prompt has been read, which alone
	// decides whether the session is a scheduled task's run.
	prompted bool
}

// transcriptRecord is the subset of one transcript line this reader needs.
// Content is kept as raw JSON and only ever inspected for its shape, and
// the first prompt's first bytes compared with the scheduled-task tag;
// it is never decoded into text, so conversation content never enters
// memory as a usable string.
type transcriptRecord struct {
	titleRecord
	Type      string `json:"type"`
	Operation string `json:"operation"`
	// Content is a queued prompt's text, kept raw like the message's, and
	// only looked at on a queue-operation record.
	Content     json.RawMessage `json:"content"`
	Timestamp   string          `json:"timestamp"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
	Message     struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Update reads whatever has been fully appended to path since the last
// call and folds it into the accumulated Stats. A missing file returns the
// stats accumulated so far along with the error.
func (r *StatsReader) Update(path string) (Stats, error) {
	if path != r.path {
		r.path = path
		r.reset()
	}

	f, err := os.Open(path)
	if err != nil {
		return r.stats, err
	}
	defer f.Close()

	if fi, statErr := f.Stat(); statErr == nil && fi.Size() < r.offset {
		// The file is now smaller than what was already read: it was
		// truncated or replaced. Start over rather than risk double
		// counting or seeking past the end of a shorter file.
		r.reset()
	}
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	if _, err := f.Seek(r.offset, 0); err != nil {
		return r.stats, err
	}

	br := bufio.NewReader(f)
	for {
		line, readErr := br.ReadBytes('\n')
		if readErr != nil {
			// Whatever came back is an incomplete final line, or nothing.
			// It is left unread so a later call sees it once it is whole.
			break
		}
		r.offset += int64(len(line))
		r.applyLine(bytes.TrimRight(line, "\r\n"))
	}
	r.updateTitle()
	return r.stats, nil
}

// reset forgets everything read so far, to start the file over.
func (r *StatsReader) reset() {
	r.offset = 0
	r.stats = Stats{}
	r.seen = nil
	r.titles = titleTracker{}
	r.pendingRead = false
	r.prompted = false
}

// updateTitle works out the title after a read. A custom-title that ended
// the previous read and still has nothing after it is taken as given.
func (r *StatsReader) updateTitle() {
	if r.titles.hasPending {
		if r.pendingRead {
			r.titles.settle()
			r.pendingRead = false
		} else {
			r.pendingRead = true
		}
	} else {
		r.pendingRead = false
	}
	r.stats.Title = sidecarTitle(r.path)
	if r.stats.Title == "" {
		r.stats.Title = r.titles.title()
	}
}

func (r *StatsReader) applyLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var rec transcriptRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	if ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil && ts.After(r.stats.LastActivity) {
		r.stats.LastActivity = ts
	}
	r.titles.record(rec.Type, &rec.titleRecord)
	// Any record read settles what was pending before it, so whatever is
	// pending now has only just been read.
	r.pendingRead = false
	switch rec.Type {
	case "assistant":
		r.applyAssistant(&rec)
	case "user":
		if isTurn(&rec) {
			r.stats.Turns++
		}
		if !rec.IsMeta && !rec.IsSidechain {
			r.firstPrompt(rec.Message.Content)
		}
	case "queue-operation":
		if rec.Operation == "enqueue" {
			r.firstPrompt(rec.Content)
		}
	}
}

// scheduledTaskTag is how Claude Desktop starts the prompt of a scheduled
// task's run, as it is written in the transcript's JSON.
var scheduledTaskTag = []byte(`"<scheduled-task `)

// scheduledContentTag is the same tag as the value of a content field, for
// a first prompt line too long to read whole.
var scheduledContentTag = []byte(`"content":"<scheduled-task `)

// firstPrompt looks at the session's first prompt, given as raw JSON, and
// notes whether it is a scheduled task's run. Later prompts change nothing.
func (r *StatsReader) firstPrompt(content json.RawMessage) {
	if r.prompted || len(content) == 0 {
		return
	}
	r.prompted = true
	if isScheduledPrompt(content) {
		r.stats.ScheduledTask = true
	}
}

// isScheduledPrompt reports whether a prompt, given as raw JSON, a string or
// a list of blocks, starts with the scheduled-task tag. Only its first bytes
// are compared; the text is never decoded.
func isScheduledPrompt(content json.RawMessage) bool {
	raw := bytes.TrimSpace(content)
	if len(raw) > 0 && raw[0] == '[' {
		var blocks []struct {
			Text json.RawMessage `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
			return false
		}
		raw = bytes.TrimSpace(blocks[0].Text)
	}
	return bytes.HasPrefix(raw, scheduledTaskTag)
}

// scheduledRunReadLimit bounds how much of a transcript ScheduledRun reads.
// The first prompt comes within the first few lines; a transcript whose
// first prompt is not within this much is taken as an ordinary session.
const scheduledRunReadLimit = 64 << 10

// ScheduledRun reports whether the transcript at path is a Claude Desktop
// scheduled task's run, from its first prompt, reading at most
// scheduledRunReadLimit bytes, so it is cheap enough to ask where a
// decision is made. ok is false when the file could not be read.
func ScheduledRun(path string) (run, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	br := bufio.NewReader(io.LimitReader(f, scheduledRunReadLimit))
	for {
		line, readErr := br.ReadBytes('\n')
		if readErr != nil {
			// A line cut off by the read limit is looked at as far as it
			// goes: a first prompt that long still starts with the tag.
			return bytes.Contains(line, scheduledContentTag), true
		}
		var rec transcriptRecord
		if json.Unmarshal(bytes.TrimSpace(line), &rec) != nil {
			continue
		}
		switch {
		case rec.Type == "user" && !rec.IsMeta && !rec.IsSidechain && len(rec.Message.Content) > 0:
			return isScheduledPrompt(rec.Message.Content), true
		case rec.Type == "queue-operation" && rec.Operation == "enqueue" && len(rec.Content) > 0:
			return isScheduledPrompt(rec.Content), true
		}
	}
}

// applyAssistant folds one assistant record's model and usage into the
// running totals. Usage is only added the first time a given message id is
// seen; a record with no id (never observed in practice, but not assumed
// impossible) is added every time.
func (r *StatsReader) applyAssistant(rec *transcriptRecord) {
	if rec.Message.Model != "" {
		r.stats.Model = rec.Message.Model
	}
	if id := rec.Message.ID; id != "" {
		if _, dup := r.seen[id]; dup {
			return
		}
		r.seen[id] = struct{}{}
	}
	r.stats.InputTokens += rec.Message.Usage.Input
	r.stats.OutputTokens += rec.Message.Usage.Output
	r.stats.CacheReadTokens += rec.Message.Usage.CacheRead
	r.stats.CacheWriteTokens += rec.Message.Usage.CacheWrite
}

// isTurn reports whether a user record represents a human prompt rather
// than bookkeeping such as a tool result, or a meta or sidechain message.
func isTurn(rec *transcriptRecord) bool {
	if rec.IsMeta || rec.IsSidechain || len(rec.Message.Content) == 0 {
		return false
	}
	var text string
	if json.Unmarshal(rec.Message.Content, &text) == nil {
		return true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" {
			return true
		}
	}
	return false
}
