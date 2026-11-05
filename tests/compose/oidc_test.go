// tests/compose/oidc_test.go
// OIDC login against a real IdP: keel-oidc in front of dex (mock connector, no login form). Keel
// reaches dex at https://dex:5556, and so does this browser-like client, by mapping that host to
// its published port: the issuer must be the same name for both.
package compose

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
)

// oidcBrowser follows redirects and keeps cookies, like a browser.
func oidcBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var d net.Dialer
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // dex's test certificate; keel verifies it with ca_file
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
			},
		},
	}
}

func navigate(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func TestCompose_OIDCLoginWithDex(t *testing.T) {
	skip(t)
	waitHTTP(t, keelURL(OIDCReady, "/readyz"), http.StatusOK, 60*time.Second)
	base := fmt.Sprintf("http://localhost:%d", OIDCHTTP)
	browser := oidcBrowser(t)

	// No session: through dex and back, ending on the page asked for.
	resp, body := navigate(t, browser, base+"/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "keel: ok") {
		t.Fatalf("login: status %d at %s, body %q", resp.StatusCode, resp.Request.URL, body)
	}
	if resp.Request.URL.Host != fmt.Sprintf("localhost:%d", OIDCHTTP) {
		t.Fatalf("login ended at %s, want keel", resp.Request.URL)
	}

	// With the session: served directly, with no trip to dex.
	noRedirect := *browser
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ = navigate(t, &noRedirect, base+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("with a session: status %d, want 200 without a redirect", resp.StatusCode)
	}

	// A different browser has no session: it is sent to the IdP.
	stranger := *oidcBrowser(t)
	stranger.CheckRedirect = noRedirect.CheckRedirect
	resp, _ = navigate(t, &stranger, base+"/")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "https://dex:5556/dex/auth") {
		t.Fatalf("no session: status %d, Location %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Logout ends the session.
	resp, _ = navigate(t, &noRedirect, base+"/oauth2/logout")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("logout: status %d", resp.StatusCode)
	}
	resp, _ = navigate(t, &noRedirect, base+"/")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("after logout: status %d, want a redirect to the IdP", resp.StatusCode)
	}
}
