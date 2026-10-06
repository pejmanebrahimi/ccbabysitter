package claude

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStatsIncremental(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	first := `{"type":"user","timestamp":"2026-09-21T10:00:00Z","message":{"role":"user","content":"hi"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-09-21T10:00:05Z","message":{"id":"msg_1","role":"assistant","model":"claude-fable-5-1","usage":{"input_tokens":120,"output_tokens":40,"cache_read_input_tokens":1000}}}` + "\n"
	if err := os.WriteFile(p, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &StatsReader{}
	s, err := r.Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Turns != 1 || s.InputTokens != 120 || s.OutputTokens != 40 || s.CacheReadTokens != 1000 || s.Model != "claude-fable-5-1" {
		t.Fatalf("%+v", s)
	}

	second := `{"type":"user","timestamp":"2026-09-21T10:01:00Z","message":{"role":"user","content":[{"type":"text","text":"go ahead"}]}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-09-21T10:01:05Z","message":{"id":"msg_2","role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":5}}}` + "\n"
	appendLine(t, p, second)

	s, err = r.Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Turns != 2 || s.InputTokens != 130 || s.OutputTokens != 45 || s.Model != "claude-sonnet-5" || s.LastActivity.Minute() != 1 {
		t.Fatalf("incremental: %+v", s)
	}
	if r.offset == 0 {
		t.Fatal("offset must advance")
	}
}

func TestStatsToleratesGarbageLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	content := "not json\n" +
		`{"type":"user","timestamp":"2026-09-21T09:00:00Z","message":{"content":"go"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-09-21T09:00:01Z","message":{"id":"msg_g","usage":{"output_tokens":3}}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (&StatsReader{}).Update(p)
	if err != nil || s.OutputTokens != 3 || s.Turns != 1 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestStatsDedupesUsageByMessageID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	line := func(ts string) string {
		return `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"msg_dup","model":"claude-sonnet-5","usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":5,"cache_creation_input_tokens":2}}}` + "\n"
	}
	content := line("2026-09-21T11:00:00Z") + line("2026-09-21T11:00:01Z") + line("2026-09-21T11:00:02Z")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (&StatsReader{}).Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.InputTokens != 50 || s.OutputTokens != 20 || s.CacheReadTokens != 5 || s.CacheWriteTokens != 2 {
		t.Fatalf("usage must be counted once per message id: %+v", s)
	}
	if s.LastActivity.Second() != 2 {
		t.Fatalf("LastActivity must still advance on every record: %+v", s)
	}
}

func TestStatsEmptyMessageIDCountsEveryRecord(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"assistant","timestamp":"2026-09-21T12:00:00Z","message":{"usage":{"output_tokens":7}}}` + "\n"
	content := line + line
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (&StatsReader{}).Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.OutputTokens != 14 {
		t.Fatalf("records without a message id must count every time: %+v", s)
	}
}

func TestStatsToolResultOnlyUserRecordIsNotATurn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	content := `{"type":"user","timestamp":"2026-09-21T13:00:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"done"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (&StatsReader{}).Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Turns != 0 {
		t.Fatalf("a tool_result-only user record must not be a turn: %+v", s)
	}
}

func TestStatsMetaAndSidechainRecordsAreNotTurns(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	content := `{"type":"user","timestamp":"2026-09-21T14:00:00Z","isMeta":true,"message":{"content":[{"type":"text","text":"setup"}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-09-21T14:00:01Z","isSidechain":true,"message":{"content":"hi"}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (&StatsReader{}).Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Turns != 0 {
		t.Fatalf("isMeta and isSidechain user records must not be turns: %+v", s)
	}
}

func TestStatsCacheCreationTokensAccumulate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	content := `{"type":"assistant","timestamp":"2026-09-21T15:00:00Z","message":{"id":"msg_cw","usage":{"cache_creation_input_tokens":250}}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (&StatsReader{}).Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.CacheWriteTokens != 250 {
		t.Fatalf("cache_creation_input_tokens must accumulate into CacheWriteTokens: %+v", s)
	}
}

func TestStatsPartialLineIsNotCountedUntilComplete(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	full := `{"type":"assistant","timestamp":"2026-09-21T16:00:00Z","message":{"id":"msg_half","usage":{"output_tokens":9}}}` + "\n"
	half := full[:len(full)/2]
	rest := full[len(full)/2:]

	if err := os.WriteFile(p, []byte(half), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &StatsReader{}
	s, err := r.Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.OutputTokens != 0 || r.offset != 0 {
		t.Fatalf("an incomplete line must not be counted yet: %+v offset=%d", s, r.offset)
	}
	if fi, statErr := os.Stat(p); statErr == nil && r.offset > fi.Size() {
		t.Fatalf("offset %d exceeds file size %d", r.offset, fi.Size())
	}

	appendLine(t, p, rest)
	s, err = r.Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.OutputTokens != 9 {
		t.Fatalf("completed line must be counted exactly once: %+v", s)
	}
	fi, statErr := os.Stat(p)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if r.offset != fi.Size() {
		t.Fatalf("offset %d must equal file size %d once every line is consumed", r.offset, fi.Size())
	}

	// Updating again with nothing new appended must not double count and
	// must never push the offset past the file size.
	s, err = r.Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.OutputTokens != 9 || r.offset > fi.Size() {
		t.Fatalf("re-reading with no new data changed stats or offset: %+v offset=%d", s, r.offset)
	}
}

func TestStatsResetsOnTruncation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	first := `{"type":"assistant","timestamp":"2026-09-21T17:00:00Z","message":{"id":"msg_before","usage":{"output_tokens":9}}}` + "\n"
	if err := os.WriteFile(p, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &StatsReader{}
	if s, err := r.Update(p); err != nil || s.OutputTokens != 9 {
		t.Fatalf("%+v %v", s, err)
	}

	second := `{"type":"assistant","timestamp":"2026-09-21T18:00:00Z","message":{"id":"msg_after","usage":{"output_tokens":4}}}` + "\n"
	if err := os.WriteFile(p, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := r.Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.OutputTokens != 4 {
		t.Fatalf("a shrunk file must reset stats and start over, got %+v", s)
	}
}

func TestStatsHandlesTwoMegabyteLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	padding := strings.Repeat("x", 2*1024*1024)
	line := `{"type":"assistant","timestamp":"2026-09-21T19:00:00Z","message":{"id":"msg_big","usage":{"output_tokens":8},"padding":"` + padding + `"}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (&StatsReader{}).Update(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.OutputTokens != 8 {
		t.Fatalf("a single very long line must still be parsed: %+v", s)
	}
}

// A reader handed a different path is reading a different conversation:
// the offset, the counted message ids and the totals from the old one
// would all be nonsense against the new one.
func TestStatsStartsOverOnADifferentPath(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.jsonl")
	second := filepath.Join(dir, "second.jsonl")
	line := func(id string, out int) string {
		return `{"type":"assistant","timestamp":"2026-09-21T21:00:00Z","message":{"id":"` + id + `","usage":{"output_tokens":` + strconv.Itoa(out) + `}}}` + "\n"
	}
	if err := os.WriteFile(first, []byte(line("msg_a", 11)+line("msg_b", 5)), 0o644); err != nil {
		t.Fatal(err)
	}
	// The second file is deliberately shorter than the first and repeats
	// one of its message ids, so a reader that kept either the offset or
	// the seen set would report the wrong number.
	if err := os.WriteFile(second, []byte(line("msg_a", 3)), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &StatsReader{}
	if s, err := r.Update(first); err != nil || s.OutputTokens != 16 {
		t.Fatalf("%+v %v", s, err)
	}
	s, err := r.Update(second)
	if err != nil {
		t.Fatal(err)
	}
	if s.OutputTokens != 3 {
		t.Fatalf("a different path must start over: %+v", s)
	}
	if s, err := r.Update(first); err != nil || s.OutputTokens != 16 {
		t.Fatalf("going back must read the first file whole again: %+v %v", s, err)
	}
}

func TestStatsMissingFileReturnsErrorAndCurrentStats(t *testing.T) {
	r := &StatsReader{}
	p := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"assistant","timestamp":"2026-09-21T20:00:00Z","message":{"id":"msg_m","usage":{"output_tokens":2}}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := r.Update(p); err != nil || s.OutputTokens != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	s, err := r.Update(p)
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if s.OutputTokens != 2 {
		t.Fatalf("a missing file must still return the stats accumulated so far: %+v", s)
	}
}

// A Claude Desktop scheduled-task run starts its conversation with a
// <scheduled-task> tag, first on the queued prompt and again on the user
// record. Only the first prompt counts: a session where the tag turns up
// later, or only further into the text, is an ordinary one.
func TestStatsTellsAScheduledTaskRun(t *testing.T) {
	queued := `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-05T05:28:47Z","content":"<scheduled-task name=\"daily-report\" file=\"/home/dev/.claude/scheduled-tasks/daily-report/SKILL.md\">\nWrite the report.\n</scheduled-task>"}` + "\n"
	dequeued := `{"type":"queue-operation","operation":"dequeue","timestamp":"2026-10-05T05:28:47Z"}` + "\n"
	asUser := `{"type":"user","timestamp":"2026-10-05T05:28:48Z","message":{"role":"user","content":"<scheduled-task name=\"daily-report\">\nWrite the report.\n</scheduled-task>"}}` + "\n"
	asBlocks := `{"type":"user","timestamp":"2026-10-05T05:28:48Z","message":{"role":"user","content":[{"type":"text","text":"<scheduled-task name=\"daily-report\">x</scheduled-task>"}]}}` + "\n"
	plain := `{"type":"user","timestamp":"2026-10-05T05:28:48Z","message":{"role":"user","content":"hi"}}` + "\n"
	quoted := `{"type":"user","timestamp":"2026-10-05T05:28:48Z","message":{"role":"user","content":"what does <scheduled-task name=\"x\"> mean?"}}` + "\n"
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"queued prompt", queued + dequeued + asUser, true},
		{"user record only", asUser, true},
		{"text blocks", asBlocks, true},
		{"an ordinary session", plain, false},
		{"the tag in a later prompt", plain + asUser, false},
		{"the tag not at the start", quoted, false},
	}
	for _, c := range cases {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := (&StatsReader{}).Update(p)
		if err != nil || s.ScheduledTask != c.want {
			t.Errorf("%s: scheduled %v, err %v; want %v", c.name, s.ScheduledTask, err, c.want)
		}
	}
	// Read in pieces, the answer is the same, and it stays once given.
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(queued), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &StatsReader{}
	if s, _ := r.Update(p); !s.ScheduledTask {
		t.Fatal("not seen from the queued prompt")
	}
	appendLine(t, p, dequeued+asUser+plain)
	if s, _ := r.Update(p); !s.ScheduledTask {
		t.Fatal("forgotten after a later read")
	}
}

// Meta and sidechain records, and queue operations other than enqueue, are
// not the first prompt; both the reader and ScheduledRun look past them. A
// reader that starts over, on a truncated file or another path, asks again.
func TestScheduledRunLooksPastWhatIsNotThePrompt(t *testing.T) {
	tag := `"<scheduled-task name=\"daily-report\">x</scheduled-task>"`
	meta := `{"type":"user","isMeta":true,"message":{"role":"user","content":"caveat"}}` + "\n"
	side := `{"type":"user","isSidechain":true,"message":{"role":"user","content":"side"}}` + "\n"
	dequeue := `{"type":"queue-operation","operation":"dequeue","content":"anything"}` + "\n"
	run := `{"type":"user","message":{"role":"user","content":` + tag + `}}` + "\n"
	plain := `{"type":"user","message":{"role":"user","content":"hi"}}` + "\n"
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, c := range map[string]struct {
		body string
		want bool
	}{
		"meta, sidechain and dequeue before a run": {meta + side + dequeue + run, true},
		"meta before an ordinary prompt":           {meta + plain + run, false},
		"nothing but bookkeeping":                  {meta + dequeue, false},
	} {
		p := write(c.body)
		if s, _ := (&StatsReader{}).Update(p); s.ScheduledTask != c.want {
			t.Errorf("reader, %s: %v, want %v", name, s.ScheduledTask, c.want)
		}
		if got, ok := ScheduledRun(p); !ok || got != c.want {
			t.Errorf("ScheduledRun, %s: %v %v, want %v", name, got, ok, c.want)
		}
	}
	if _, ok := ScheduledRun(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Error("a missing file was read")
	}

	// A file rewritten shorter, now a run, is asked about again.
	p := write(plain + plain + plain)
	r := &StatsReader{}
	if s, _ := r.Update(p); s.ScheduledTask {
		t.Fatal("an ordinary session read as a run")
	}
	if err := os.WriteFile(p, []byte(run), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, _ := r.Update(p); !s.ScheduledTask {
		t.Fatal("a reader starting over on a truncated file did not ask again")
	}
	// So is another path.
	if s, _ := r.Update(write(plain)); s.ScheduledTask {
		t.Fatal("a reader on another path kept the old answer")
	}
	if s, _ := r.Update(write(run)); !s.ScheduledTask {
		t.Fatal("a reader on another path did not ask again")
	}
}

// A first prompt line longer than the read limit still starts with the tag,
// and is a run.
func TestScheduledRunWithAPromptPastTheReadLimit(t *testing.T) {
	long := strings.Repeat("x", scheduledRunReadLimit+1000)
	body := `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-05T05:28:47Z","content":"<scheduled-task name=\"daily-report\">` + long + `</scheduled-task>"}` + "\n"
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if run, ok := ScheduledRun(p); !ok || !run {
		t.Fatalf("run %v ok %v", run, ok)
	}
	plain := `{"type":"user","message":{"role":"user","content":"` + long + `"}}` + "\n"
	if err := os.WriteFile(p, []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}
	if run, _ := ScheduledRun(p); run {
		t.Fatal("a long ordinary prompt read as a run")
	}
}
