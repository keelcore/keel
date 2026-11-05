package tlspolicy

import (
	"crypto/tls"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClientConfig_IsTheListenerPolicy(t *testing.T) {
	c := ClientConfig()
	if c.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %x, want TLS 1.3", c.MinVersion)
	}
	if b := Base(); len(c.CurvePreferences) != len(b.CurvePreferences) {
		t.Fatalf("curves %v, policy %v", c.CurvePreferences, b.CurvePreferences)
	}
}

// Callers layer RootCAs, ServerName and client certs on their own copy.
func TestClientConfig_IsACopy(t *testing.T) {
	a, b := ClientConfig(), ClientConfig()
	a.ServerName = "x"
	if b.ServerName != "" {
		t.Fatal("ClientConfig shares state between callers")
	}
}

func TestNewHTTPTransport(t *testing.T) {
	tr := NewHTTPTransport()
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("TLSClientConfig = %+v, want the policy", tr.TLSClientConfig)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Fatal("HTTP/2 must stay available with a custom TLS config")
	}
	if dt, ok := http.DefaultTransport.(*http.Transport); ok && dt.TLSClientConfig == tr.TLSClientConfig {
		t.Fatal("the default transport was modified")
	}
}

func TestNewHTTPClient(t *testing.T) {
	c := NewHTTPClient(3 * time.Second)
	if c.Timeout != 3*time.Second {
		t.Fatalf("Timeout = %s", c.Timeout)
	}
	if tr, ok := c.Transport.(*http.Transport); !ok || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("Transport = %#v, want the policy", c.Transport)
	}
}

// TestOutboundTLSPolicy_NoOtherPath: non-test code in pkg/ and cmd/ reaches the network only
// through this package: no tls.Config or http.Transport literal, no default transport or client,
// no http.Client without an explicit Transport, no package-level http.Get and friends.
func TestOutboundTLSPolicy_NoOtherPath(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	var findings []string
	for _, dir := range []string{"pkg", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
				strings.Contains(path, filepath.Join("core", "tlspolicy")) {
				return nil
			}
			findings = append(findings, scan(t, path)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(findings) > 0 {
		t.Fatalf("outbound TLS outside pkg/core/tlspolicy:\n%s", strings.Join(findings, "\n"))
	}
}

// scan reports the file's direct uses of crypto/tls and net/http client plumbing.
func scan(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = p
	}
	selector := func(e ast.Expr) string {
		if s, ok := e.(*ast.SelectorExpr); ok {
			if x, ok := s.X.(*ast.Ident); ok {
				return names[x.Name] + "." + s.Sel.Name
			}
		}
		return ""
	}
	var out []string
	report := func(n ast.Node, what string) {
		out = append(out, fset.Position(n.Pos()).String()+": "+what)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CompositeLit:
			switch selector(v.Type) {
			case "crypto/tls.Config":
				report(v, "tls.Config literal")
			case "net/http.Transport":
				report(v, "http.Transport literal")
			case "net/http.Client":
				if !hasField(v, "Transport") {
					report(v, "http.Client without a Transport")
				}
			}
		case *ast.SelectorExpr:
			switch selector(v) {
			case "net/http.DefaultTransport", "net/http.DefaultClient", "net/http.Get", "net/http.Post",
				"net/http.PostForm", "net/http.Head":
				report(v, selector(v))
			}
		}
		return true
	})
	return out
}

func hasField(lit *ast.CompositeLit, name string) bool {
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == name {
				return true
			}
		}
	}
	return false
}
