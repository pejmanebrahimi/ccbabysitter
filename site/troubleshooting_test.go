package site

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
	"ccbabysitter.dev/ccbabysitter/internal/web"
)

// troubleWriters are the packages whose Activity errors and automatic
// actions the troubleshooting page answers, with the method names that
// write them. The demo's scripted lines are not CC Babysitter's own.
var troubleWriters = []struct {
	dir   string
	calls map[string]bool
}{
	{filepath.Join("..", "internal", "supervise"), map[string]bool{"logAuto": true, "logError": true, "Auto": true, "Error": true}},
	{filepath.Join("..", "internal", "observe"), map[string]bool{"Error": true}},
	{filepath.Join("..", "internal", "web"), map[string]bool{"Error": true, "Auto": true}},
	{filepath.Join("..", "cmd", "ccbabysitter"), map[string]bool{"Error": true}},
}

// troubleFiles are files every message of which is in scope: the reasons
// a session is brought back for, and what a resume answers, both written
// to Activity on an automatic action.
var troubleFiles = []string{"cause.go", "resume.go"}

// troubleUnread are message arguments the reading cannot follow, each with
// where its messages are read instead. A new one fails the test until it
// is added here with that reason, or written so it can be followed.
var troubleUnread = map[string]string{
	"reconcile.go fallback: res.Message":         "what a resume answers: troubleFiles has resume.go",
	"reconcile.go adoptRunningCopy: res.Message": "what a resume answers: troubleFiles has resume.go",
	"reconcile.go fallback: reason":              "why the session went down: troubleFiles has cause.go, and exitReason's idle reason is named below",
	"supervisor.go New: report.Reason":           "why saved state was set aside: the heal constants of internal/state",
	"supervisor.go logAuto: reason":              "the wrapper passes on what its callers give",
	"supervisor.go logAuto: msg":                 "the wrapper passes on what its callers give",
	"supervisor.go logError: msg":                "the wrapper passes on what its callers give",
	"observer.go watch: msg":                     "the file watcher's own lines, which all start with file watcher",
}

// troublePageNotes are what the page itself says when something is wrong,
// which the test checks are still in app.js.
var troublePageNotes = []string{
	"Not running. Start CC Babysitter to reconnect.",
	"CC Babysitter refused that request. Open this page from the address it printed when it started.",
	"Too many pages are connected to CC Babysitter. Close one and try again.",
	"CC Babysitter did not answer. It may have stopped running.",
	"three starts failed in five minutes",
	"Not responding in",
}

// troubleElsewhere are quotes on the page that are not CC Babysitter's
// own: what Claude Desktop says.
var troubleElsewhere = []string{"That session is running in the background"}

// pkgSource is one package's syntax: its files, constants and functions.
type pkgSource struct {
	fset   *token.FileSet
	files  map[string]*ast.File
	consts map[string]string
	funcs  map[string]*ast.FuncDecl
}

func readPackage(t *testing.T, dir string) *pkgSource {
	t.Helper()
	p := &pkgSource{fset: token.NewFileSet(), files: map[string]*ast.File{}, consts: map[string]string{}, funcs: map[string]*ast.FuncDecl{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") || n == "demo.go" {
			continue
		}
		f, err := parser.ParseFile(p.fset, filepath.Join(dir, n), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		p.files[n] = f
		for _, d := range f.Decls {
			switch x := d.(type) {
			case *ast.FuncDecl:
				p.funcs[x.Name.Name] = x
			case *ast.GenDecl:
				if x.Tok != token.CONST {
					continue
				}
				for _, sp := range x.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, nm := range vs.Names {
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								if s, err := strconv.Unquote(lit.Value); err == nil {
									p.consts[nm.Name] = s
								}
							}
						}
					}
				}
			}
		}
	}
	return p
}

// assignments are the expressions each local name is set to in a
// function, every one of them; an addition to a name with += is not a new
// beginning.
func assignments(body *ast.BlockStmt) map[string][]ast.Expr {
	vars := map[string][]ast.Expr{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if x.Tok != token.ASSIGN && x.Tok != token.DEFINE {
				return true
			}
			for i, l := range x.Lhs {
				if id, ok := l.(*ast.Ident); ok && i < len(x.Rhs) && len(x.Lhs) == len(x.Rhs) {
					vars[id.Name] = append(vars[id.Name], x.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, nm := range x.Names {
				if i < len(x.Values) {
					vars[nm.Name] = append(vars[nm.Name], x.Values[i])
				}
			}
		}
		return true
	})
	return vars
}

// texts are the words a message expression can start with, followed
// through constants, every assignment of a local name, fmt.Sprintf and the
// string functions of the package. ok is false when part of it cannot be
// followed.
func (p *pkgSource) texts(e ast.Expr, vars map[string][]ast.Expr, seen map[string]bool) ([]string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			if s, err := strconv.Unquote(x.Value); err == nil {
				return []string{s}, true
			}
		}
	case *ast.ParenExpr:
		return p.texts(x.X, vars, seen)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			if out, ok := p.texts(x.X, vars, seen); ok {
				return out, true
			}
			// A message that starts with a name, as a session's label,
			// is quoted by its first words of its own.
			return p.texts(x.Y, vars, seen)
		}
	case *ast.Ident:
		if s, ok := p.consts[x.Name]; ok {
			return []string{s}, true
		}
		if es, ok := vars[x.Name]; ok {
			var out []string
			for _, v := range es {
				got, ok := p.texts(v, vars, seen)
				if !ok {
					return nil, false
				}
				out = append(out, got...)
			}
			return out, true
		}
	case *ast.CallExpr:
		var name string
		switch f := x.Fun.(type) {
		case *ast.SelectorExpr:
			if id, ok := f.X.(*ast.Ident); ok && id.Name == "fmt" && len(x.Args) > 0 {
				return p.texts(x.Args[0], vars, seen)
			}
			name = f.Sel.Name
		case *ast.Ident:
			name = f.Name
		}
		if fn, ok := p.funcs[name]; ok && fn.Body != nil {
			if seen[name] {
				return nil, true
			}
			seen[name] = true
			return p.returns(fn, seen)
		}
	}
	return nil, false
}

// returns are the words the first results of a function's returns can
// start with.
func (p *pkgSource) returns(fn *ast.FuncDecl, seen map[string]bool) ([]string, bool) {
	vars := assignments(fn.Body)
	var out []string
	ok := true
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, inner := n.(*ast.FuncLit); inner {
			return false
		}
		r, isReturn := n.(*ast.ReturnStmt)
		if !isReturn || len(r.Results) == 0 {
			return true
		}
		got, good := p.texts(r.Results[0], vars, seen)
		if !good {
			if lit, isLit := r.Results[0].(*ast.BasicLit); !isLit || lit.Value != `""` {
				ok = false
			}
		}
		out = append(out, got...)
		return true
	})
	return out, ok
}

// troubleKey is the part of a message the page has to quote: its words up
// to the first thing filled in, a %s, a value added on or a command in
// backticks, without the spaces and punctuation around it.
func troubleKey(s string) string {
	if i := strings.IndexAny(s, "%`"); i >= 0 {
		s = s[:i]
	}
	return strings.Trim(s, " :,.(`")
}

// troubleMessages are the messages the troubleshooting page answers, read
// from the code, and the message arguments it could not follow and that
// troubleUnread does not name.
func troubleMessages(t *testing.T) (keys []string, unread []string) {
	t.Helper()
	found := map[string]bool{}
	add := func(s string) {
		if k := troubleKey(s); len(k) >= 8 {
			found[k] = true
		}
	}
	for _, w := range troubleWriters {
		p := readPackage(t, w.dir)
		for name, f := range p.files {
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				vars := assignments(fn.Body)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) < 2 {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || !w.calls[sel.Sel.Name] {
						return true
					}
					// http.Error answers a browser; it is not an Activity line.
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "http" {
						return true
					}
					for _, a := range call.Args[1:] {
						got, ok := p.texts(a, vars, map[string]bool{})
						if !ok {
							where := name + " " + fn.Name.Name + ": " + exprString(p.fset, a)
							if _, known := troubleUnread[where]; !known {
								unread = append(unread, where)
							}
						}
						for _, s := range got {
							add(s)
						}
					}
					return true
				})
			}
		}
	}
	sup := readPackage(t, filepath.Join("..", "internal", "supervise"))
	for _, name := range troubleFiles {
		f := sup.files[name]
		for _, d := range f.Decls {
			if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
				for _, sp := range g.Specs {
					for _, nm := range sp.(*ast.ValueSpec).Names {
						if s, ok := sup.consts[nm.Name]; ok {
							add(s)
						}
					}
				}
			}
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// The whole messages: what a resume answers, and what the
			// file's functions return as words.
			vars := assignments(fn.Body)
			got, _ := sup.returns(fn, map[string]bool{})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if kv, ok := n.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Message" {
						more, _ := sup.texts(kv.Value, vars, map[string]bool{})
						got = append(got, more...)
					}
				}
				return true
			})
			for _, s := range got {
				add(s)
			}
		}
	}
	state := readPackage(t, filepath.Join("..", "internal", "state"))
	for n, s := range state.consts {
		if strings.HasPrefix(n, "heal") {
			add(s)
		}
	}
	for _, s := range []string{supervise.FallbackHome, supervise.UntrustedWarning("/w"), supervise.StartNotLoggedIn, web.KeyMessage} {
		add(strings.SplitAfter(s, ". ")[0])
	}
	for _, h := range []claude.Host{claude.HostTerminal, claude.HostDesktop, claude.HostVSCode, claude.HostBackground} {
		add(supervise.RCHint(h, "1a2b3c4d"))
	}
	for _, s := range troublePageNotes {
		add(s)
	}
	// The reason exitReason gives itself, when Claude Code stopped a session
	// that sat idle.
	add("Claude Code stopped it after")
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sort.Strings(unread)
	return keys, unread
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	start, end := fset.Position(e.Pos()).Offset, fset.Position(e.End()).Offset
	src, err := os.ReadFile(fset.Position(e.Pos()).Filename)
	if err != nil || end > len(src) {
		return ""
	}
	b.Write(src[start:end])
	return b.String()
}

// pageQuotes are the messages the troubleshooting page quotes, in its
// lists of messages. A quote that ends in " ..." is the start of a longer
// message, and is read without that ending.
var pageQuote = regexp.MustCompile(`(?s)<ul class="msgs">(.*?)</ul>`)
var quoteItem = regexp.MustCompile(`<li><code>(.*?)</code></li>`)

func pageQuotes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, list := range pageQuote.FindAllStringSubmatch(readSite(t, "docs/troubleshooting/index.html"), -1) {
		for _, m := range quoteItem.FindAllStringSubmatch(list[1], -1) {
			out = append(out, strings.TrimSuffix(html.UnescapeString(m[1]), " ..."))
		}
	}
	return out
}

// Every Activity error, every automatic action, with the reasons a session
// is brought back for and what a resume answers, and every warning a card
// or the page shows is quoted on the troubleshooting page. A message the
// reading cannot follow fails the test until it is named in troubleUnread.
func TestTroubleshootingCoversEveryMessage(t *testing.T) {
	keys, unread := troubleMessages(t)
	for _, u := range unread {
		t.Errorf("a message the test cannot read: %s; write it so it can be followed, or name it in troubleUnread", u)
	}
	quotes := strings.Join(pageQuotes(t), "\n")
	for _, k := range keys {
		if !strings.Contains(quotes, k) {
			t.Errorf("the troubleshooting page does not quote %q", k)
		}
	}
	app, err := os.ReadFile(filepath.Join("..", "internal", "web", "ui", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range troublePageNotes {
		if !strings.Contains(string(app), s) {
			t.Errorf("app.js no longer says %q", s)
		}
	}
}

// Every message the page quotes is still in the code, so a message that
// changed or went away does not stay on the page.
func TestTroubleshootingQuotesAreCurrent(t *testing.T) {
	var code strings.Builder
	for _, root := range []string{filepath.Join("..", "internal"), filepath.Join("..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return err
			}
			if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".html") {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				code.Write(b)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	src := code.String()
	for _, q := range pageQuotes(t) {
		if contains(troubleElsewhere, q) {
			continue
		}
		if !strings.Contains(src, q) {
			t.Errorf("the troubleshooting page quotes %q, which the code no longer says", q)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
