package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/client"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// The exit codes, as the usage promises them.
const (
	exitRefused    = 1
	exitUsage      = 2
	exitNotRunning = 3
	exitNoSession  = 4
)

// controlEnv is everything a control command takes from the machine, so a
// test can aim it at its own server and its own process table.
type controlEnv struct {
	stateDir string
	self     func(v supervise.View) (int, bool) // real: client.FindSelf(v, os.Getpid(), procs.NewReal())
	headless bool                               // real: runtime.GOOS == "linux" && hosts.Headless()
	stdout   io.Writer
	stderr   io.Writer
}

// realControlEnv is the env of this process on this machine.
func realControlEnv() controlEnv {
	// The client trusts the saved page address only while a live copy
	// holds the state folder's lock, which needs the real process check.
	state.SetPIDChecker(procs.NewReal().Exists)
	return controlEnv{
		stateDir: state.DefaultDir(),
		self: func(v supervise.View) (int, bool) {
			return client.FindSelf(v, os.Getpid(), procs.NewReal())
		},
		headless: runtime.GOOS == "linux" && hosts.Headless(),
		stdout:   os.Stdout,
		stderr:   os.Stderr,
	}
}

// The JSON documents the commands print. Each carries the schema it follows,
// and fields are only ever added.
type (
	errorDoc struct {
		Schema int    `json:"schema"`
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Code   string `json:"code"`
	}
	notRunningDoc struct {
		Schema  int  `json:"schema"`
		Running bool `json:"running"`
	}
	statusDoc struct {
		Schema    int       `json:"schema"`
		Running   bool      `json:"running"`
		Version   string    `json:"version"`
		URL       string    `json:"url"`
		Headless  bool      `json:"headless"`
		Sessions  int       `json:"sessions"`
		Babysat   int       `json:"babysat"`
		KeepAwake keepAwake `json:"keepAwake"`
	}
	keepAwake struct {
		Held      bool `json:"held"`
		Supported bool `json:"supported"`
	}
	listDoc struct {
		Schema   int              `json:"schema"`
		Sessions []client.Session `json:"sessions"`
	}
	showDoc struct {
		Schema  int            `json:"schema"`
		Session client.Session `json:"session"`
	}
	resultDoc struct {
		Schema  int    `json:"schema"`
		OK      bool   `json:"ok"`
		Message string `json:"message"`
		Session string `json:"session,omitempty"`
	}
	activityDoc struct {
		Schema  int             `json:"schema"`
		Entries []activityEntry `json:"entries"`
	}
	activityEntry struct {
		Time      string `json:"time"`
		Level     string `json:"level"`
		Session   string `json:"session"`
		Automatic bool   `json:"automatic"`
		Reason    string `json:"reason"`
		Message   string `json:"message"`
	}
	settingsDoc struct {
		Schema   int `json:"schema"`
		Settings struct {
			Autostart   bool   `json:"autostart"`
			AutoBabysit bool   `json:"autoBabysit"`
			OpenBrowser bool   `json:"openBrowser"`
			Theme       string `json:"theme"`
		} `json:"settings"`
	}
)

// controlArgs is a control command's command line, read.
type controlArgs struct {
	json, yes, startAtLogin bool
	url                     string
	n                       int      // activity, default 20
	pos                     []string // the positional words
}

// controlCommands are the commands that talk to the running copy.
var controlCommands = map[string]bool{
	"status": true, "list": true, "show": true, "babysit": true, "unbabysit": true,
	"retry": true, "stop": true, "activity": true, "settings": true, "quit": true,
}

func isControlCommand(name string) bool { return controlCommands[name] }

// flagName is the name a token spells as a flag, without its dashes or
// its =value, and false when the token is a word.
func flagName(tok string) (string, bool) {
	if len(tok) < 2 || tok[0] != '-' {
		return "", false
	}
	name := strings.TrimPrefix(strings.TrimPrefix(tok, "-"), "-")
	name, _, _ = strings.Cut(name, "=")
	return name, true
}

// parseControlArgs reads the command line of a control command. Go's flag
// package stops at the first word that is not a flag, so stop api --yes
// would never see --yes: the flags are split from the words first and
// parsed on their own, which lets them stand anywhere.
func parseControlArgs(name string, args []string) (controlArgs, error) {
	var flagToks, pos []string
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		fname, isFlag := flagName(tok)
		if !isFlag {
			pos = append(pos, tok)
			continue
		}
		flagToks = append(flagToks, tok)
		takesValue := fname == "url" || (fname == "n" && name == "activity")
		if takesValue && !strings.Contains(tok, "=") {
			if i+1 >= len(args) {
				if fname == "n" {
					return controlArgs{}, errors.New("-n needs a number")
				}
				return controlArgs{}, errors.New("--url needs a page address")
			}
			i++
			flagToks = append(flagToks, args[i])
		}
	}

	a := controlArgs{n: 20}
	var n string
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&a.json, "json", false, "")
	fs.StringVar(&a.url, "url", "", "")
	switch name {
	case "babysit":
		fs.BoolVar(&a.startAtLogin, "start-at-login", false, "")
	case "stop":
		fs.BoolVar(&a.yes, "yes", false, "")
	case "activity":
		fs.StringVar(&n, "n", "", "")
	}
	if err := fs.Parse(flagToks); err != nil {
		return controlArgs{}, flagError(err)
	}
	if n != "" {
		v, err := strconv.Atoi(n)
		if err != nil || v < 1 {
			return controlArgs{}, errors.New("-n needs a number")
		}
		a.n = v
	}
	a.pos = pos

	switch name {
	case "status", "list", "quit":
		if len(pos) != 0 {
			return controlArgs{}, fmt.Errorf("%s takes no words, but got %s", name, pos[0])
		}
	case "show", "babysit", "unbabysit", "retry", "stop":
		if len(pos) != 1 {
			return controlArgs{}, fmt.Errorf("%s needs one session", name)
		}
	case "activity":
		if len(pos) > 1 {
			return controlArgs{}, errors.New("activity takes at most one session")
		}
	case "settings":
		if len(pos) != 0 && len(pos) != 2 {
			return controlArgs{}, errors.New("settings takes a name and a value, or nothing to show them")
		}
		if len(pos) == 2 {
			if _, err := settingPatch(pos[0], pos[1]); err != nil {
				return controlArgs{}, err
			}
		}
	}
	return a, nil
}

// flagError turns the flag package's complaint into a plain sentence.
func flagError(err error) error {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
		return fmt.Errorf("unknown flag --%s", rest)
	}
	return errors.New(msg)
}

// settingPatch is the request body that sets one setting, or the usage
// error that names what would have been valid.
func settingPatch(name, value string) (map[string]any, error) {
	key := ""
	switch name {
	case "autostart":
		key = "autostart"
	case "auto-babysit":
		key = "autoBabysit"
	case "open-browser":
		key = "autoOpenBrowser"
	case "theme":
		if !state.ValidTheme(value) {
			return nil, fmt.Errorf("theme is auto, dark or light, not %s", value)
		}
		return map[string]any{"theme": value}, nil
	default:
		return nil, fmt.Errorf("no setting is called %s. The settings are autostart, auto-babysit, open-browser and theme", name)
	}
	switch value {
	case "on":
		return map[string]any{key: true}, nil
	case "off":
		return map[string]any{key: false}, nil
	}
	return nil, fmt.Errorf("%s is on or off, not %s", name, value)
}

// runControl runs one control command and returns its exit code.
func runControl(name string, args []string, env controlEnv) int {
	if wantsHelp(args) {
		printCommandHelp(env.stdout, name)
		return 0
	}
	a, err := parseControlArgs(name, args)
	if err != nil {
		return env.fail(wantsJSON(args), exitUsage, "usage",
			fmt.Sprintf("%s. See ccbabysitter help %s.", err, name))
	}
	c, err := client.New(env.stateDir, a.url)
	if errors.Is(err, client.ErrNotRunning) {
		return env.failErr(a.json, name == "status", "", err)
	}
	if err != nil {
		// A --url that is not a page address, which client.New words itself.
		return env.fail(a.json, exitUsage, "usage", err.Error())
	}
	ctx := context.Background()

	// quit needs no view: it only asks the running copy to stop.
	if name == "quit" {
		res, err := c.Quit(ctx)
		if err != nil {
			return env.failErr(a.json, false, "", err)
		}
		return env.printResult(a.json, res, "")
	}

	// activity without a session only needs the log.
	if name == "activity" && len(a.pos) == 0 {
		entries, err := c.Activity(ctx, a.n)
		if err != nil {
			return env.failErr(a.json, false, "", err)
		}
		return env.printActivity(a.json, entries)
	}

	view, err := c.View(ctx)
	if err != nil {
		return env.failErr(a.json, name == "status", "", err)
	}
	self := func() (int, bool) {
		if env.self == nil {
			return 0, false
		}
		return env.self(view)
	}

	switch name {
	case "status":
		// status is how a person gets the address with the key again, so
		// it gives that one.
		return env.printStatus(a.json, c.KeyedURL(), view)
	case "list":
		return env.printList(a.json, view)
	case "settings":
		if len(a.pos) == 0 {
			return env.printSettings(a.json, view.Settings)
		}
		patch, _ := settingPatch(a.pos[0], a.pos[1])
		res, err := c.SetSettings(ctx, patch)
		if err != nil {
			return env.failErr(a.json, false, "", err)
		}
		return env.printResult(a.json, res, "")
	}

	s, err := client.Resolve(view, a.pos[0], self)
	if err != nil {
		return env.failErr(a.json, false, a.pos[0], err)
	}
	switch name {
	case "show":
		return env.printShow(a.json, s)
	case "activity":
		entries, err := c.Activity(ctx, 500)
		if err != nil {
			return env.failErr(a.json, false, "", err)
		}
		return env.printActivity(a.json, forSession(entries, s, a.n))
	}

	var res supervise.Result
	switch name {
	case "babysit":
		res, err = c.Babysit(ctx, s.ID, a.startAtLogin)
	case "unbabysit":
		res, err = c.Unbabysit(ctx, s.ID)
	case "retry":
		res, err = c.Retry(ctx, s.ID)
	case "stop":
		if !a.yes {
			return env.fail(a.json, exitUsage, "usage", fmt.Sprintf(
				"This stops the background copy of %s (%s). The conversation is kept. Run it again with --yes to confirm.",
				firstOf(s.Name, s.ShortID), s.ShortID))
		}
		res, err = c.Stop(ctx, s.ID)
	}
	if err != nil {
		return env.failErr(a.json, false, "", err)
	}
	return env.printResult(a.json, res, s.ShortID)
}

func firstOf(words ...string) string {
	for _, w := range words {
		if w != "" {
			return w
		}
	}
	return ""
}

// wantsHelp reports whether args ask for the command's help page, which
// agents try first, before anything is parsed or any server is asked.
func wantsHelp(args []string) bool {
	for _, tok := range args {
		switch tok {
		case "--":
			return false
		case "--help", "-help", "-h":
			return true
		}
	}
	return false
}

// wantsJSON reports whether --json is among args, for a command line that
// could not be read any further.
func wantsJSON(args []string) bool {
	for _, tok := range args {
		if tok == "--" {
			return false
		}
		if tok == "--json" || tok == "-json" {
			return true
		}
	}
	return false
}

// writeJSON writes doc as the one document on stdout.
func (env controlEnv) writeJSON(doc any) {
	enc := json.NewEncoder(env.stdout)
	enc.SetEscapeHTML(false)
	enc.Encode(doc)
}

// fail reports an error: its sentence on stderr, and with --json also the
// error document on stdout.
func (env controlEnv) fail(asJSON bool, exit int, code, msg string) int {
	fmt.Fprintln(env.stderr, msg)
	if asJSON {
		env.writeJSON(errorDoc{Schema: 1, Error: msg, Code: code})
	}
	return exit
}

// failErr reports what went wrong with err. For status, not running is the
// answer rather than an error, so --json prints that instead of the error
// document. word is what the person typed to name a session.
func (env controlEnv) failErr(asJSON, isStatus bool, word string, err error) int {
	var amb *client.AmbiguousError
	var apiErr *client.Error
	switch {
	case errors.Is(err, client.ErrNotRunning):
		msg := "CC Babysitter is not running. Start it with: ccbabysitter"
		if env.headless {
			msg = "CC Babysitter is not running. Start its service with: systemctl --user start ccbabysitter"
		}
		if isStatus && asJSON {
			fmt.Fprintln(env.stderr, msg)
			env.writeJSON(notRunningDoc{Schema: 1})
			return exitNotRunning
		}
		return env.fail(asJSON, exitNotRunning, "not-running", msg)
	case errors.Is(err, client.ErrNoKey):
		// Something answered, so it is running: what is missing is the
		// key, and the sentence says where to get it.
		return env.fail(asJSON, exitRefused, "no-key", client.ErrNoKey.Error())
	case errors.Is(err, client.ErrNoAnswer):
		return env.fail(asJSON, exitRefused, "no-answer",
			"CC Babysitter did not answer in time. The action may still finish. See ccbabysitter activity.")
	case errors.Is(err, client.ErrNotFound):
		return env.fail(asJSON, exitNoSession, "not-found",
			fmt.Sprintf("No session matches %s. See ccbabysitter list.", word))
	case errors.As(err, &amb):
		return env.fail(asJSON, exitNoSession, "ambiguous", amb.Error()+". Use one of the ids.")
	case errors.Is(err, client.ErrNotInSession):
		return env.fail(asJSON, exitNoSession, "not-in-session",
			"This command is not running inside a Claude Code session, so self means nothing here. Name the session instead, see ccbabysitter list.")
	case errors.As(err, &apiErr):
		return env.fail(asJSON, exitRefused, "failed", apiErr.Message)
	}
	return env.fail(asJSON, exitRefused, "failed", err.Error())
}

func (env controlEnv) printStatus(asJSON bool, url string, v supervise.View) int {
	sessions := client.Sessions(v)
	babysat := 0
	for _, s := range sessions {
		if s.Babysat {
			babysat++
		}
	}
	if asJSON {
		env.writeJSON(statusDoc{
			Schema: 1, Running: true, Version: v.Version, URL: url, Headless: v.Env.Headless,
			Sessions: len(sessions), Babysat: babysat,
			KeepAwake: keepAwake{Held: v.KeepAwake.Held, Supported: v.KeepAwake.Supported},
		})
		return 0
	}
	awake := "no"
	switch {
	case !v.KeepAwake.Supported:
		awake = "not supported on this system"
	case v.KeepAwake.Held:
		awake = "yes"
	}
	fmt.Fprintf(env.stdout, "CC Babysitter %s is running at %s\n", v.Version, url)
	fmt.Fprintf(env.stdout, "Sessions: %d, babysat: %d. Keeping the computer awake: %s\n", len(sessions), babysat, awake)
	return 0
}

// babysatWords is how a babysat session's state reads in text.
func babysatWords(s client.Session) string {
	switch {
	case !s.Babysat:
		return "no"
	case s.State == string(supervise.StateInBackground):
		return "in background"
	case s.State == "":
		return "yes"
	}
	return s.State
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// oneLine keeps a value, such as a name someone typed, from breaking the
// line or the column it is printed in.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

func (env controlEnv) printList(asJSON bool, v supervise.View) int {
	sessions := client.Sessions(v)
	if asJSON {
		env.writeJSON(listDoc{Schema: 1, Sessions: sessions})
		return 0
	}
	if len(sessions) == 0 {
		fmt.Fprintln(env.stdout, "No Claude Code sessions are running.")
		return 0
	}
	rows := [][]string{{"ID", "NAME", "APP", "RC", "BABYSAT", "TOKENS", "UPTIME", "FOLDER"}}
	for _, s := range sessions {
		uptime := "-"
		if s.Running {
			uptime = fmtDuration(s.UptimeSeconds)
		}
		t := s.Tokens
		rows = append(rows, []string{
			oneLine(s.ShortID), firstOf(oneLine(s.Name), "-"), firstOf(s.App, "-"), onOff(s.RemoteControl),
			babysatWords(s), fmtTokens(t.Input + t.Output + t.CacheRead + t.CacheWrite), uptime, firstOf(oneLine(s.Folder), "-"),
		})
	}
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, cell := range r {
			widths[i] = max(widths[i], len(cell))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, cell := range r {
			if i == len(r)-1 {
				b.WriteString(cell)
				break
			}
			b.WriteString(cell + strings.Repeat(" ", widths[i]-len(cell)+2))
		}
		fmt.Fprintln(env.stdout, strings.TrimRight(b.String(), " "))
	}
	return 0
}

func (env controlEnv) printShow(asJSON bool, s client.Session) int {
	if asJSON {
		env.writeJSON(showDoc{Schema: 1, Session: s})
		return 0
	}
	var also []string
	for _, app := range s.Apps {
		if app != s.App {
			also = append(also, app)
		}
	}
	t := s.Tokens
	last := ""
	if at, err := time.Parse(time.RFC3339, s.LastActivity); err == nil {
		last = at.Local().Format(localTime)
	}
	uptime := ""
	if s.Running {
		uptime = fmtDuration(s.UptimeSeconds)
	}
	watch := ""
	if s.Babysat {
		watch = babysatWords(s)
	}
	fields := []struct{ label, value string }{
		{"Id", s.ID},
		{"Short id", s.ShortID},
		{"Name", oneLine(s.Name)},
		{"Also called", oneLine(s.AlsoCalled)},
		{"Folder", oneLine(s.Folder)},
		{"App", firstOf(s.App, "none")},
		{"Also running in", strings.Join(also, ", ")},
		{"Running", yesNo(s.Running)},
		{"Remote Control", onOff(s.RemoteControl)},
		{"Status", s.Status},
		{"Babysat", yesNo(s.Babysat)},
		{"State", watch},
		{"Tokens", fmt.Sprintf("in %s, out %s, cache %s", fmtTokens(t.Input), fmtTokens(t.Output), fmtTokens(t.CacheRead+t.CacheWrite))},
		{"Model", s.Model},
		{"Last activity", last},
		{"Uptime", uptime},
		{"Open with Remote Control", s.RemoteURL},
		{"Attach", s.AttachCmd},
		{"Attach over ssh", s.SSHAttachCmd},
		{"Resume", s.ResumeCmd},
		{"To switch Remote Control on", s.RCHint},
		{"Warning", oneLine(s.Warning)},
	}
	for _, f := range fields {
		if f.value != "" {
			fmt.Fprintf(env.stdout, "%s: %s\n", f.label, f.value)
		}
	}
	return 0
}

// printResult answers an action: on stdout when it was done, on stderr
// when it was refused. session is the short id, empty for settings.
func (env controlEnv) printResult(asJSON bool, res supervise.Result, session string) int {
	msg := res.Message
	if msg == "" && res.OK && session == "" {
		msg = "Saved."
	}
	exit := 0
	if !res.OK {
		exit = exitRefused
	}
	if asJSON {
		env.writeJSON(resultDoc{Schema: 1, OK: res.OK, Message: msg, Session: session})
		return exit
	}
	if res.OK {
		fmt.Fprintln(env.stdout, msg)
	} else {
		fmt.Fprintln(env.stderr, msg)
	}
	return exit
}

// localTime is how the command line writes a time of day for a person.
const localTime = "2006-01-02 15:04:05"

// forSession keeps the entries that are about s, at most n of them. The
// log names a session by its name or by its short id.
func forSession(entries []state.Entry, s client.Session, n int) []state.Entry {
	labels := make(map[string]bool)
	for _, l := range s.Labels() {
		labels[l] = true
	}
	var out []state.Entry
	for _, e := range entries {
		if labels[e.Session] && len(out) < n {
			out = append(out, e)
		}
	}
	return out
}

func (env controlEnv) printActivity(asJSON bool, entries []state.Entry) int {
	if asJSON {
		doc := activityDoc{Schema: 1, Entries: make([]activityEntry, 0, len(entries))}
		for _, e := range entries {
			doc.Entries = append(doc.Entries, activityEntry{
				Time: e.Time.Format(time.RFC3339), Level: e.Level, Session: e.Session,
				Automatic: e.Automatic, Reason: e.Reason, Message: e.Message,
			})
		}
		env.writeJSON(doc)
		return 0
	}
	for _, e := range entries {
		line := fmt.Sprintf("%s  %s  %s", e.Time.Local().Format(localTime), firstOf(oneLine(e.Session), "-"), oneLine(e.Message))
		if e.Automatic && e.Reason != "" {
			line += "  (automatic: " + oneLine(e.Reason) + ")"
		}
		fmt.Fprintln(env.stdout, line)
	}
	return 0
}

func (env controlEnv) printSettings(asJSON bool, st state.Settings) int {
	if asJSON {
		doc := settingsDoc{Schema: 1}
		doc.Settings.Autostart = st.Autostart
		doc.Settings.AutoBabysit = st.AutoBabysit
		doc.Settings.OpenBrowser = st.AutoOpenBrowser
		doc.Settings.Theme = st.Theme
		env.writeJSON(doc)
		return 0
	}
	fmt.Fprintf(env.stdout, "autostart: %s\nauto-babysit: %s\nopen-browser: %s\ntheme: %s\n",
		onOff(st.Autostart), onOff(st.AutoBabysit), onOff(st.AutoOpenBrowser), st.Theme)
	return 0
}
