package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

// The SIGUSR1 dump must never carry HMAC secrets or URL credentials/tokens.
func TestRedactedConfig_NoSecretsInDump(t *testing.T) {
	var cfg config.Config
	cfg.Authn.TrustedSigners = []string{"hmac-super-secret", "https://idp.example/jwks?token=t0k3n"}
	cfg.Authn.MySignatureKeyFile = "/etc/keel/signer.pem"
	cfg.Sidecar.UpstreamURL = "http://user:pa55@upstream:9000/"
	cfg.ExtAuthz.Endpoint = "https://authz.example/check?api_key=k3y"
	cfg.Logging.RemoteSink.Endpoint = "https://logs.example/ingest?token=sinktok"
	cfg.Tracing.OTLP.Endpoint = "https://otlp.example:4318"
	cfg.Metrics.StatsD.Endpoint = "127.0.0.1:8125"
	cfg.TLS.ACME.CAUrl = "https://acme.example/dir"

	b, err := json.Marshal(redactedConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, secret := range []string{"hmac-super-secret", "t0k3n", "pa55", "user:", "k3y", "sinktok"} {
		if strings.Contains(out, secret) {
			t.Errorf("dump leaks %q: %s", secret, out)
		}
	}
	for _, kept := range []string{"/etc/keel/signer.pem", "upstream:9000", "idp.example/jwks", "otlp.example:4318", "127.0.0.1:8125", "acme.example/dir"} {
		if !strings.Contains(out, kept) {
			t.Errorf("dump lost non-secret %q: %s", kept, out)
		}
	}
	if cfg.Authn.TrustedSigners[0] != "hmac-super-secret" {
		t.Error("redaction mutated the live config")
	}
}

func TestRedactURL_UnparsableUnchanged(t *testing.T) {
	if got := redactURL("http://[::1"); got != "http://[::1" {
		t.Errorf("got %q", got)
	}
}
