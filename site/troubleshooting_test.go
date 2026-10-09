package site

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/supervise"
	"ccbabysitter.dev/ccbabysitter/internal/web"
)

// troubleSources are the Go files whose Activity errors and automatic
// actions the troubleshooting page answers, with the calls that write them.
var troubleSources = []struct {
	dir    string
	calls  map[string]bool // method names whose string arguments are messages
	consts string          // the name prefix of constants that are messages
}{
	{filepath.Join("..", "internal", "supervise"), map[string]bool{"logAuto": true, "logError": true}, ""},
	{filepath.Join("..", "internal", "observe"), map[string]bool{"Error": true}, ""},
	{filepath.Join("..", "internal", "web"), map[string]bool{"Error": true, "Auto": true}, ""},
	{filepath.Join("..", "cmd", "ccbabysitter"), map[string]bool{"Error": true}, ""},
	// Why saved state was set aside, which Activity writes as an error.
	{filepath.Join("..", "internal", "state"), nil, "heal"},
}

// leadingText is the words a message expression starts with: a string
// literal, the first literal of a + chain, a package constant, or a
// variable set from one of those in the same function. Anything else, such
// as a call, has no words of its own here.
func leadingText(e ast.Expr, consts map[string]string, vars map[string]ast.Expr) string {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			if err == nil {
				return s
			}
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return leadingText(x.X, consts, vars)
		}
	case *ast.Ident:
		if s, ok := consts[x.Name]; ok {
			return s
		}
		if v, ok := vars[x.Name]; ok {
			return leadingText(v, consts, nil)
		}
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sprintf" && len(x.Args) > 0 {
			return leadingText(x.Args[0], consts, vars)
		}
	}
	return ""
}

// troubleKey is the part of a message the page has to quote: its words up
// to the first thing filled in, a %s or a value added on, without the
// spaces and punctuation that lead into it.
func troubleKey(s string) string {
	if i := strings.Index(s, "%"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, " :,.(")
}

// troubleMessages are the Activity messages the troubleshooting page
// answers, read from the code: every error, and every reason and message
// of an automatic action, with the reasons a session is brought back for.
func troubleMessages(t *testing.T) []string {
	t.Helper()
	keys := map[string]bool{}
	add := func(s string) {
		if k := troubleKey(s); len(k) >= 8 {
			keys[k] = true
		}
	}
	for _, src := range troubleSources {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, src.dir, func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, pkg := range pkgs {
			consts := map[string]string{}
			for _, f := range pkg.Files {
				for _, d := range f.Decls {
					g, ok := d.(*ast.GenDecl)
					if !ok || g.Tok != token.CONST {
						continue
					}
					for _, sp := range g.Specs {
						vs := sp.(*ast.ValueSpec)
						for i, n := range vs.Names {
							if i < len(vs.Values) {
								if s := leadingText(vs.Values[i], nil, nil); s != "" {
									consts[n.Name] = s
								}
							}
						}
					}
				}
			}
			if src.consts != "" {
				for n, s := range consts {
					if strings.HasPrefix(n, src.consts) {
						add(s)
					}
				}
			}
			for name, f := range pkg.Files {
				// The reasons a session is brought back for are the
				// constants of the file that works them out.
				if filepath.Base(name) == "cause.go" {
					for _, d := range f.Decls {
						if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
							for _, sp := range g.Specs {
								for _, n := range sp.(*ast.ValueSpec).Names {
									if s, ok := consts[n.Name]; ok {
										add(s)
									}
								}
							}
						}
					}
				}
				ast.Inspect(f, func(n ast.Node) bool {
					fn, ok := n.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						return true
					}
					vars := map[string]ast.Expr{}
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						switch x := n.(type) {
						case *ast.AssignStmt:
							for i, l := range x.Lhs {
								if id, ok := l.(*ast.Ident); ok && i < len(x.Rhs) {
									vars[id.Name] = x.Rhs[i]
								}
							}
						case *ast.CallExpr:
							sel, ok := x.Fun.(*ast.SelectorExpr)
							if !ok || !src.calls[sel.Sel.Name] || len(x.Args) < 2 {
								return true
							}
							// The first argument is the session the line is
							// about; the rest are what it says.
							for _, a := range x.Args[1:] {
								add(leadingText(a, consts, vars))
							}
						}
						return true
					})
					return false
				})
			}
		}
	}
	// What the page and the cards say.
	for _, s := range []string{
		supervise.FallbackHome, supervise.UntrustedWarning("/w"), supervise.StartNotLoggedIn, web.KeyMessage,
		"resumed as background", "Not responding in",
		// Made by functions the reading above cannot follow: the freeze's
		// reason, the reason after an idle stop, and a reason set twice.
		"not responding: busy for", "Claude Code stopped it after", "a saved conversation is there now",
	} {
		add(strings.SplitAfter(s, ". ")[0])
	}
	var out []string
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Every Activity error, every automatic action and every warning a card or
// the page shows has an entry on the troubleshooting page, which quotes it.
func TestTroubleshootingCoversEveryMessage(t *testing.T) {
	page := html.UnescapeString(readSite(t, "docs/troubleshooting/index.html"))
	for _, k := range troubleMessages(t) {
		if !strings.Contains(page, k) {
			t.Errorf("the troubleshooting page does not quote %q", k)
		}
	}
}
