package claude

import (
	"time"
	"strings"
	"testing"
)

const desktopFile = `{"pid":120,"sessionId":"11111111-2222-4333-8444-555555555501","cwd":"C:\\Users\\dev\\ws","procStart":"134000000000000000","version":"2.1.266","kind":"interactive","entrypoint":"claude-desktop","name":"demo-a1","bridgeSessionId":"session_TESTBRIDGE01"}`
const bgFile = `{"pid":121,"sessionId":"22222222-2222-4333-8444-555555555502","cwd":"C:\\ws","procStart":"134000000000000001","kind":"background","entrypoint":"cli","name":"demo-b2","jobId":"33333333","status":"idle","bridgeSessionId":"session_TESTBRIDGE02"}`
const cliFile = `{"pid":5,"sessionId":"11111111-2222-4333-8444-555555555502","cwd":"/home/dev/ws","procStart":"1","kind":"interactive","entrypoint":"cli"}`
const vscodeFile = `{"pid":7,"sessionId":"aaaaaaaa-2222-4333-8444-555555555502","cwd":"/home/dev/ws","procStart":"1","kind":"interactive","entrypoint":"claude-vscode"}`
const macFile = `{"pid":122,"sessionId":"44444444-5555-4666-8777-555555555504","cwd":"/home/dev/ws","procStart":"Mon Sep 21 15:52:16 2026","kind":"interactive","entrypoint":"cli","name":"demo-c3","hostSessionId":"host-abc123","pidDomain":"user"}`

func TestParseSessionFile(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		host      Host
		short     string
		rc        bool
		status    string
		sname     string
		procStart string
	}{
		{"desktop", desktopFile, HostDesktop, "11111111", true, "", "demo-a1", "134000000000000000"},
		{"background uses jobId", bgFile, HostBackground, "33333333", true, "idle", "demo-b2", "134000000000000001"},
		{"terminal", cliFile, HostTerminal, "11111111", false, "", "", "1"},
		{"vscode", vscodeFile, HostVSCode, "aaaaaaaa", false, "", "", "1"},
		{"macos procStart is a timestamp string", macFile, HostTerminal, "44444444", false, "", "demo-c3", "Mon Sep 21 15:52:16 2026"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := ParseSessionFile([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if s.Host != c.host || s.ShortID != c.short || s.RemoteControl != c.rc || s.Status != c.status || s.Name != c.sname || s.ProcStart != c.procStart {
				t.Fatalf("got %+v", s)
			}
		})
	}
}

func TestParseSessionFileProcStartVariants(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"bare number",
			`{"pid":1,"sessionId":"55555555-6666-4777-8888-555555555505","cwd":"/home/dev/ws","procStart":134000000000000000}`,
			"134000000000000000",
		},
		{
			"null",
			`{"pid":1,"sessionId":"66666666-7777-4888-8999-555555555506","cwd":"/home/dev/ws","procStart":null}`,
			"",
		},
		{
			"missing",
			`{"pid":1,"sessionId":"77777777-8888-4999-8aaa-555555555507","cwd":"/home/dev/ws"}`,
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := ParseSessionFile([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if s.ProcStart != c.want {
				t.Fatalf("ProcStart = %q, want %q", s.ProcStart, c.want)
			}
		})
	}
}

func TestParseSessionFileRejects(t *testing.T) {
	for _, in := range []string{
		"", "{bad", "{}",
		`{"pid":1,"cwd":"x"}`,
		`{"pid":"abc","sessionId":"11111111-2222-4333-8444-555555555501","cwd":"x"}`,
		// A session id that is not a canonical UUID reaches a command line
		// and a file path, so the whole file is skipped.
		`{"pid":1,"sessionId":"abcdefgh-1","cwd":"x"}`,
		`{"pid":1,"sessionId":"a-1","cwd":"x"}`,
		`{"pid":1,"sessionId":"11111111-2222-4333-8444-55555555550g","cwd":"x"}`,
	} {
		if _, err := ParseSessionFile([]byte(in)); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
	s, err := ParseSessionFile([]byte(`{"pid":1,"sessionId":"11111111-2222-4333-8444-555555555501","cwd":"x","bridgeSessionId":""}`))
	if err != nil || s.RemoteControl {
		t.Fatalf("empty bridge id must mean off: %+v %v", s, err)
	}
}

// A jobId reaches a command a person is invited to copy and run, so only
// one of exactly the right shape is believed; anything else falls back to
// the session id's own first eight characters, which are hex by
// construction.
func TestParseSessionFileShortIDIsAlwaysEightLowercaseHex(t *testing.T) {
	cases := []struct {
		jobID string
		want  string
	}{
		{`"33333333"`, "33333333"},
		{`"AB12CD34"`, "ab12cd34"},
		{`""`, "11111111"},
		{`"not-hex!"`, "11111111"},
		{`"3333333"`, "11111111"},
		{`"333333333"`, "11111111"},
		{`"a1b2c3d4; rm -rf /"`, "11111111"},
		{`"../../etc"`, "11111111"},
	}
	for _, c := range cases {
		in := `{"pid":1,"sessionId":"11111111-2222-4333-8444-555555555501","cwd":"/home/dev/ws","jobId":` + c.jobID + `}`
		s, err := ParseSessionFile([]byte(in))
		if err != nil {
			t.Fatalf("jobId %s: %v", c.jobID, err)
		}
		if s.ShortID != c.want {
			t.Errorf("jobId %s: ShortID = %q, want %q", c.jobID, s.ShortID, c.want)
		}
	}

	upper := `{"pid":1,"sessionId":"AAAAAAAA-2222-4333-8444-555555555501","cwd":"/home/dev/ws"}`
	s, err := ParseSessionFile([]byte(upper))
	if err != nil {
		t.Fatal(err)
	}
	if s.ShortID != "aaaaaaaa" {
		t.Fatalf("a short id taken from an upper case id must be lowered: %q", s.ShortID)
	}
}

func TestParseSessionFileCapsNameAndCwd(t *testing.T) {
	long := strings.Repeat("n", 500)
	deep := "/home/dev/" + strings.Repeat("d", 5000)
	in := `{"pid":1,"sessionId":"11111111-2222-4333-8444-555555555501","cwd":"` + deep + `","name":"` + long + `"}`
	s, err := ParseSessionFile([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(s.Name)) != 200 {
		t.Fatalf("Name must be capped at 200 runes, got %d", len([]rune(s.Name)))
	}
	if len(s.Cwd) != 4096 {
		t.Fatalf("Cwd must be capped at 4096 bytes, got %d", len(s.Cwd))
	}
}

func TestClassifyHost(t *testing.T) {
	cases := map[[2]string]Host{
		{"cli", "interactive"}:               HostTerminal,
		{"cli", "bg"}:                        HostBackground,
		{"cli", "background"}:                HostBackground,
		{"claude-desktop", "interactive"}:    HostDesktop,
		{"claude-desktop-3p", "interactive"}: HostDesktop,
		{"claude-vscode", "interactive"}:     HostVSCode,
		{"local-agent", "interactive"}:       HostOther,
		{"sdk-ts", ""}:                       HostOther,
		// An entrypoint that says nothing is not something this program
		// knows how to close and resume, so it is left alone.
		{"", ""}:            HostOther,
		{"", "background"}:  HostOther,
		{"", "interactive"}: HostOther,
	}
	for in, want := range cases {
		if got := ClassifyHost(in[0], in[1]); got != want {
			t.Errorf("ClassifyHost(%q,%q)=%q want %q", in[0], in[1], got, want)
		}
	}
}

// startedAt is when the session started in its process, in milliseconds.
// A background process is started ahead of time and given to a session
// later, so it can be much later than the process's own start.
func TestParseSessionFileStartedAt(t *testing.T) {
	with := `{"pid":121,"sessionId":"22222222-2222-4333-8444-555555555502","cwd":"/w","procStart":"1","kind":"bg","startedAt":1791554370322}`
	s, err := ParseSessionFile([]byte(with))
	if err != nil {
		t.Fatal(err)
	}
	if !s.StartedAt.Equal(time.UnixMilli(1791554370322)) {
		t.Fatalf("StartedAt %v", s.StartedAt)
	}
	for _, in := range []string{bgFile,
		`{"pid":121,"sessionId":"22222222-2222-4333-8444-555555555502","cwd":"/w","procStart":"1","startedAt":"soon"}`,
		`{"pid":121,"sessionId":"22222222-2222-4333-8444-555555555502","cwd":"/w","procStart":"1","startedAt":-5}`} {
		s, err := ParseSessionFile([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !s.StartedAt.IsZero() {
			t.Fatalf("%s: StartedAt %v, want none", in, s.StartedAt)
		}
	}
}
