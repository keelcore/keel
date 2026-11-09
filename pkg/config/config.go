// pkg/config/config.go
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/keelcore/knobs"

	"go.yaml.in/yaml/v3"

	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/ports"
)

// ---------------------------------------------------------------------------
// Duration — wraps time.Duration for clean YAML "5s" / "30s" notation.
// ---------------------------------------------------------------------------

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"5s\": %w", err)
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dur
	return nil
}

func DurationOf(d time.Duration) Duration { return Duration{d} }

// ---------------------------------------------------------------------------
// Sub-structs (hierarchical, matching keel.yaml schema in README §3.8.1)
// ---------------------------------------------------------------------------

type ListenerConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type ListenersConfig struct {
	HTTP    ListenerConfig `yaml:"http"`
	HTTPS   ListenerConfig `yaml:"https"`
	H3      ListenerConfig `yaml:"h3"`
	Health  ListenerConfig `yaml:"health"`
	Ready   ListenerConfig `yaml:"ready"`
	Startup ListenerConfig `yaml:"startup"`
	Admin   ListenerConfig `yaml:"admin"`
}

type ACMEConfig struct {
	Enabled       bool     `yaml:"enabled"`
	Domains       []string `yaml:"domains"`
	Email         string   `yaml:"email"`
	CacheDir      string   `yaml:"cache_dir"`
	CAUrl         string   `yaml:"ca_url"`
	CACertFile    string   `yaml:"ca_cert_file"`
	ChallengePort int      `yaml:"challenge_port"`
}

type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// ClientCAFile, when set, makes HTTPS and H3 require a client certificate
	// that chains to a CA in this PEM bundle (inbound mTLS). Restart-required.
	ClientCAFile string     `yaml:"client_ca_file"`
	ACME         ACMEConfig `yaml:"acme"`
	// Notice is where clients rejected for their protocol are sent to read why (docs/security.md).
	Notice NoticeConfig `yaml:"notice"`
	// ClientCertNotice, with client_ca_file set, completes the handshake without a
	// client certificate and answers every request on it with the KEEL-PROTO-005
	// rejection; a presented but invalid certificate still fails the handshake.
	ClientCertNotice bool `yaml:"client_cert_notice"`
}

// NoticeConfig holds the operator's optional strings for protocol rejections.
type NoticeConfig struct {
	BaseURL string `yaml:"base_url"` // scheme://host[:port] serving the notice; empty: the HTTP listener
	HelpURL string `yaml:"help_url"` // linked from the notice page
	Contact string `yaml:"contact"`  // shown on the notice page
}

type UpstreamTLSConfig struct {
	Enabled            bool   `yaml:"enabled"`
	CAFile             string `yaml:"ca_file"`
	ClientCertFile     string `yaml:"client_cert_file"`
	ClientKeyFile      string `yaml:"client_key_file"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

type CircuitBreakerConfig struct {
	Enabled          bool     `yaml:"enabled"`
	FailureThreshold int      `yaml:"failure_threshold"`
	ResetTimeout     Duration `yaml:"reset_timeout"`
}

// SidecarRetryConfig bounds retries of upstream dials that fail before any
// request byte is sent, which makes them safe for every method.
type SidecarRetryConfig struct {
	DialAttempts int      `yaml:"dial_attempts"`
	DialBackoff  Duration `yaml:"dial_backoff"`
	// HonorRetryAfter retries a 503 or 429 that carries Retry-After once, after the
	// stated delay, when the request can be replayed and the delay fits the deadline.
	HonorRetryAfter bool `yaml:"honor_retry_after"`
}

type HeaderPolicyConfig struct {
	Forward []string `yaml:"forward"`
	Strip   []string `yaml:"strip"`
}

type SidecarConfig struct {
	Enabled     bool   `yaml:"enabled"`
	UpstreamURL string `yaml:"upstream_url"` // http(s)://host:port or unix:///path/app.sock
	// UpstreamH2C speaks plaintext HTTP/2 (prior knowledge) to the upstream.
	UpstreamH2C            bool                 `yaml:"upstream_h2c"`
	UpstreamAudience       string               `yaml:"upstream_audience"`
	UpstreamHealthPath     string               `yaml:"upstream_health_path"`
	UpstreamHealthInterval Duration             `yaml:"upstream_health_interval"`
	UpstreamHealthTimeout  Duration             `yaml:"upstream_health_timeout"`
	UpstreamTLS            UpstreamTLSConfig    `yaml:"upstream_tls"`
	CircuitBreaker         CircuitBreakerConfig `yaml:"circuit_breaker"`
	Retry                  SidecarRetryConfig   `yaml:"retry"`
	HeaderPolicy           HeaderPolicyConfig   `yaml:"header_policy"`
	XFFMode                string               `yaml:"xff_mode"`
	XFFTrustedHops         int                  `yaml:"xff_trusted_hops"`
}

type SecurityConfig struct {
	OWASPHeaders         bool  `yaml:"owasp_headers"`
	MaxHeaderBytes       int   `yaml:"max_header_bytes"`
	MaxRequestBodyBytes  int64 `yaml:"max_request_body_bytes"`
	MaxResponseBodyBytes int64 `yaml:"max_response_body_bytes"`
	HSTSMaxAge           int   `yaml:"hsts_max_age"`
	// AltSvcMaxAge is the lifetime (seconds) of the Alt-Svc advertisement of HTTP/3.
	AltSvcMaxAge int `yaml:"alt_svc_max_age"`
	// MinHTTPVersion is the lowest HTTP version served on HTTPS and H3: "1.1", "2" or "3".
	MinHTTPVersion string `yaml:"min_http_version"`
}

type TimeoutsConfig struct {
	ReadHeader    Duration `yaml:"read_header"`
	Read          Duration `yaml:"read"`
	Write         Duration `yaml:"write"`
	Idle          Duration `yaml:"idle"`
	ShutdownDrain Duration `yaml:"shutdown_drain"`
	PrestopSleep  Duration `yaml:"prestop_sleep"`
}

type LimitsConfig struct {
	MaxConcurrent int `yaml:"max_concurrent"`
	QueueDepth    int `yaml:"queue_depth"`
}

type BackpressureConfig struct {
	HeapMaxBytes    int64   `yaml:"heap_max_bytes"`
	HighWatermark   float64 `yaml:"high_watermark"`
	LowWatermark    float64 `yaml:"low_watermark"`
	SheddingEnabled bool    `yaml:"shedding_enabled"`
	// MinHeadroomBytes: shed requests (503) while memory headroom below the
	// effective limit is under this floor; 0 disables the memory guard.
	MinHeadroomBytes int64 `yaml:"min_headroom_bytes"`
}

type AuthnConfig struct {
	Enabled            bool     `yaml:"enabled"`
	TrustedIDs         []string `yaml:"trusted_ids"`
	TrustedSigners     []string `yaml:"trusted_signers"`
	TrustedSignersFile string   `yaml:"trusted_signers_file"`
	MyID               string   `yaml:"my_id"`
	MySignatureKeyFile string   `yaml:"my_signature_key_file"`
	Audience           string   `yaml:"audience"`
	// OIDC is the browser login flow (authorization code with PKCE).
	OIDC OIDCConfig `yaml:"oidc"`
}

// OIDCConfig configures the OpenID Connect login flow (docs/security.md §10).
type OIDCConfig struct {
	Enabled       bool     `yaml:"enabled"`
	Issuer        string   `yaml:"issuer"`
	ClientID      string   `yaml:"client_id"`
	ClientSecret  string   `yaml:"client_secret"`
	RedirectURL   string   `yaml:"redirect_url"`
	Scopes        []string `yaml:"scopes"`
	CookieKeyFile string   `yaml:"cookie_key_file"`
	CAFile        string   `yaml:"ca_file"`
	SessionTTL    Duration `yaml:"session_ttl"`
}

type ExtAuthzConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Endpoint  string   `yaml:"endpoint"` // http(s)://host/path  or  unix:///path/to/socket
	Path      string   `yaml:"path"`     // request path when endpoint is a unix socket
	Timeout   Duration `yaml:"timeout"`
	Transport string   `yaml:"transport"` // "http" (default) or "opa"
	FailOpen  bool     `yaml:"fail_open"` // true = allow on error; false = deny (default)
}

type RemoteSinkConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
	Protocol string `yaml:"protocol"`
}

type LoggingConfig struct {
	JSON       bool             `yaml:"json"`
	Level      string           `yaml:"level"`
	AccessLog  bool             `yaml:"access_log"`
	RemoteSink RemoteSinkConfig `yaml:"remote_sink"`
	// File, when set, also writes every log line to this path, rotated per Rotation.
	File     string            `yaml:"file"`
	Rotation LogRotationConfig `yaml:"rotation"`
}

// AuditConfig is the tamper-evident audit stream (docs/security.md), separate from the logs.
type AuditConfig struct {
	Enabled    bool              `yaml:"enabled"`
	File       string            `yaml:"file"`
	Rotation   LogRotationConfig `yaml:"rotation"`
	RemoteSink RemoteSinkConfig  `yaml:"remote_sink"`
}

// LogRotationConfig bounds logging.file. Zero turns a limit off.
type LogRotationConfig struct {
	MaxSizeMB  int  `yaml:"max_size_mb"`
	MaxAgeDays int  `yaml:"max_age_days"`
	MaxBackups int  `yaml:"max_backups"`
	Compress   bool `yaml:"compress"`
}

type StatsDConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
	Prefix   string `yaml:"prefix"`
}

type MetricsConfig struct {
	Prometheus bool         `yaml:"prometheus"`
	StatsD     StatsDConfig `yaml:"statsd"`
}

type OTLPConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
	Insecure bool   `yaml:"insecure"`
}

type TracingConfig struct {
	OTLP OTLPConfig `yaml:"otlp"`
}

// AdminConfig controls access to the admin listener's privileged endpoints.
// Probes, /version, and /fips are always open; /metrics is open unless
// MetricsToken is set; /admin/reload needs Token; /debug/pprof/* needs PProf
// and Token. Tokens are secrets: set them in keel-secrets.yaml or via env.
type AdminConfig struct {
	Token        string `yaml:"token"`
	MetricsToken string `yaml:"metrics_token"`
	PProf        bool   `yaml:"pprof"`
}

type FIPSConfig struct {
	Monitor bool `yaml:"monitor"`
}

// ---------------------------------------------------------------------------
// Top-level Config
// ---------------------------------------------------------------------------

type Config struct {
	Listeners    ListenersConfig    `yaml:"listeners"`
	TLS          TLSConfig          `yaml:"tls"`
	Sidecar      SidecarConfig      `yaml:"sidecar"`
	Security     SecurityConfig     `yaml:"security"`
	Timeouts     TimeoutsConfig     `yaml:"timeouts"`
	Limits       LimitsConfig       `yaml:"limits"`
	Backpressure BackpressureConfig `yaml:"backpressure"`
	Authn        AuthnConfig        `yaml:"authn"`
	ExtAuthz     ExtAuthzConfig     `yaml:"ext_authz"`
	Logging      LoggingConfig      `yaml:"logging"`
	Audit        AuditConfig        `yaml:"audit"`
	Metrics      MetricsConfig      `yaml:"metrics"`
	Tracing      TracingConfig      `yaml:"tracing"`
	FIPS         FIPSConfig         `yaml:"fips"`
	Admin        AdminConfig        `yaml:"admin"`
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

// Defaults returns a Config pre-populated with all library defaults.
// Downstream callers should use this to initialise their own config structs
// before unmarshaling their YAML so absent keys retain library defaults.
func Defaults() Config { return defaults() }

func defaults() Config {
	return Config{
		Listeners: ListenersConfig{
			HTTP:    ListenerConfig{Enabled: true, Port: ports.HTTP},
			HTTPS:   ListenerConfig{Enabled: false, Port: ports.HTTPS},
			H3:      ListenerConfig{Enabled: false, Port: ports.H3},
			Health:  ListenerConfig{Enabled: true, Port: ports.HEALTH},
			Ready:   ListenerConfig{Enabled: true, Port: ports.READY},
			Startup: ListenerConfig{Enabled: false, Port: ports.STARTUP},
			Admin:   ListenerConfig{Enabled: false, Port: ports.ADMIN},
		},
		Security: SecurityConfig{
			OWASPHeaders:         true,
			MaxHeaderBytes:       65536,
			MaxRequestBodyBytes:  10 << 20, // 10 MB
			MaxResponseBodyBytes: 50 << 20, // 50 MB
			HSTSMaxAge:           63072000,
			AltSvcMaxAge:         86400,
			MinHTTPVersion:       "1.1",
		},
		Timeouts: TimeoutsConfig{
			ReadHeader:    DurationOf(5 * time.Second),
			Read:          DurationOf(30 * time.Second),
			Write:         DurationOf(30 * time.Second),
			Idle:          DurationOf(60 * time.Second),
			ShutdownDrain: DurationOf(10 * time.Second),
			PrestopSleep:  DurationOf(0),
		},
		Backpressure: BackpressureConfig{
			HighWatermark:    0.85,
			LowWatermark:     0.70,
			SheddingEnabled:  true,
			MinHeadroomBytes: 10 << 20,
		},
		Authn: AuthnConfig{
			Enabled: true,
			OIDC: OIDCConfig{
				Scopes:     []string{"openid", "email", "profile"},
				SessionTTL: DurationOf(8 * time.Hour),
			},
		},
		ExtAuthz: ExtAuthzConfig{
			Timeout:   DurationOf(500 * time.Millisecond),
			Transport: "http",
		},
		Logging: LoggingConfig{
			JSON:      true,
			Level:     "info",
			AccessLog: true,
			Rotation:  LogRotationConfig{MaxSizeMB: 100, MaxAgeDays: 30, MaxBackups: 5, Compress: true},
		},
		Audit: AuditConfig{
			Rotation: LogRotationConfig{MaxSizeMB: 100, Compress: true},
		},
		Metrics: MetricsConfig{
			Prometheus: true,
			StatsD:     StatsDConfig{Prefix: "keel"},
		},
		TLS: TLSConfig{
			ACME: ACMEConfig{ChallengePort: 80},
		},
		FIPS: FIPSConfig{
			Monitor: false,
		},
		Sidecar: SidecarConfig{
			UpstreamHealthPath:     "/health",
			UpstreamHealthInterval: DurationOf(10 * time.Second),
			UpstreamHealthTimeout:  DurationOf(2 * time.Second),
			CircuitBreaker: CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 5,
				ResetTimeout:     DurationOf(30 * time.Second),
			},
			Retry: SidecarRetryConfig{
				DialAttempts: 3,
				DialBackoff:  DurationOf(50 * time.Millisecond),
			},
			XFFMode: "append",
		},
	}
}

// ---------------------------------------------------------------------------
// Load — keelcore/knobs resolves every layer over the code defaults: the
// global and user files, the secrets file, env and --knob arguments.
// ---------------------------------------------------------------------------

// cliKnobs are the --knob arguments (keel.<path>=<value>): the cli layer of
// every Load and From, startup and reload alike.
var cliKnobs []string

// SetKnobs records the --knob arguments for every later Load and From.
func SetKnobs(args []string) { cliKnobs = args }

// Default loads and validates the configuration from the locations knobs
// resolves itself. On error it calls log.Fatal; it never returns an error.
func Default(log *logging.Logger) Config {
	cfg, err := Load("", "")
	if err != nil {
		log.Fatal("config_load_failed", map[string]any{"err": err.Error()})
	}
	return cfg
}

// Load resolves the configuration through keelcore/knobs and validates it.
// configPath and secretsPath, when set, name the global and secrets files;
// empty, knobs resolves them: the path knobs keel.knobs.global_path and
// keel.knobs.secrets_path (--knob, then KEEL_KNOBS_GLOBAL_PATH and
// KEEL_KNOBS_SECRETS_PATH), else /etc/keel/config.yaml and no secrets file.
// Use Default for CLI entry points.
func Load(configPath, secretsPath string) (Config, error) {
	cfg, err := load(configPath, secretsPath)
	if err != nil {
		return cfg, err
	}
	if err := Validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// From resolves env and --knob overrides over c, validates the result,
// and returns it. Intended for library users who supply their own Config.
func From(c *Config) (Config, error) {
	cfg := *c
	e, err := newEngine()
	if err != nil {
		return cfg, err
	}
	_ = e.Add(knobs.Env(os.LookupEnv), knobs.CLI(cliKnobs)) // Add fails only after Resolve
	if err := resolve(e, &cfg); err != nil {
		return cfg, err
	}
	if err := Validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// load resolves knobs's standard layers over the code defaults.
func load(configPath, secretsPath string) (Config, error) {
	cfg := defaults()
	e, err := newEngine()
	if err != nil {
		return cfg, err
	}
	if err := e.AddStandard(knobs.Standard{
		GlobalPath:  configPath,
		SecretsPath: secretsPath,
		Env:         os.LookupEnv, // until knobs reads the environment itself (keelcore/knobs#14)
		Knobs:       cliKnobs,
	}); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	return cfg, resolve(e, &cfg)
}

// Validate returns an error if the config contains an invalid combination
// or violates the constraints declared in pkg/config/schema.yaml.
func Validate(cfg Config) error {
	if err := validateProtocolNotice(cfg); err != nil {
		return err
	}
	if err := validateLogRotation(cfg.Logging.Rotation); err != nil {
		return err
	}
	if err := validateAudit(cfg.Audit); err != nil {
		return err
	}
	if err := validateOIDC(cfg.Authn.OIDC); err != nil {
		return err
	}
	httpsWantsCert := cfg.Listeners.HTTPS.Enabled || cfg.Listeners.H3.Enabled
	acme := cfg.TLS.ACME.Enabled
	hasCert := cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != ""

	if httpsWantsCert && !acme && !hasCert {
		return fmt.Errorf("HTTPS/H3 enabled but no TLS cert/key configured and ACME is disabled")
	}
	if acme && hasCert {
		return fmt.Errorf("ACME is enabled but cert_file/key_file are also set; leave them empty when using ACME")
	}
	if acme && len(cfg.TLS.ACME.Domains) == 0 {
		return fmt.Errorf("ACME is enabled but no domains are configured")
	}
	if acme && cfg.TLS.ACME.ChallengePort != 80 && os.Getenv("BATS_TEST_FILENAME") == "" {
		return fmt.Errorf("tls.acme.challenge_port must be 80 in production (RFC 8555 §8.3); got %d", cfg.TLS.ACME.ChallengePort)
	}
	if cfg.Backpressure.HighWatermark > 0 && cfg.Backpressure.LowWatermark >= cfg.Backpressure.HighWatermark {
		return fmt.Errorf("backpressure.low_watermark (%.2f) must be less than high_watermark (%.2f)",
			cfg.Backpressure.LowWatermark, cfg.Backpressure.HighWatermark)
	}
	if cfg.Sidecar.Enabled && cfg.Sidecar.UpstreamURL == "" {
		return fmt.Errorf("sidecar is enabled but upstream_url is not set")
	}
	// TLS to the upstream excludes both a unix socket and plaintext h2c.
	if cfg.Sidecar.UpstreamTLS.Enabled && strings.HasPrefix(cfg.Sidecar.UpstreamURL, "unix://") {
		return fmt.Errorf("sidecar.upstream_tls cannot be used with a unix:// upstream_url")
	}
	if cfg.Sidecar.UpstreamTLS.Enabled && cfg.Sidecar.UpstreamH2C {
		return fmt.Errorf("sidecar.upstream_h2c (plaintext HTTP/2) cannot be used with upstream_tls")
	}
	if cfg.Sidecar.Enabled && cfg.Sidecar.Retry.DialAttempts < 1 {
		return fmt.Errorf("sidecar.retry.dial_attempts must be at least 1; got %d", cfg.Sidecar.Retry.DialAttempts)
	}
	if cfg.Sidecar.Enabled && cfg.Sidecar.Retry.DialBackoff.Duration < 0 {
		return fmt.Errorf("sidecar.retry.dial_backoff must not be negative (%s)", cfg.Sidecar.Retry.DialBackoff.Duration)
	}
	// Either gap makes every authz call fail, which fail_open turns into
	// allow-all; reject the config instead.
	if cfg.ExtAuthz.Enabled && cfg.ExtAuthz.Endpoint == "" {
		return fmt.Errorf("ext_authz is enabled but endpoint is not set")
	}
	if cfg.ExtAuthz.Enabled && cfg.ExtAuthz.Timeout.Duration <= 0 {
		return fmt.Errorf("ext_authz is enabled but timeout is not positive (%s)", cfg.ExtAuthz.Timeout.Duration)
	}
	return nil
}

// ---------------------------------------------------------------------------
// keelcore/knobs — the engine and the values it resolves.
// ---------------------------------------------------------------------------

// newEngine returns a knobs engine for the keel application with the embedded
// schema attached.
func newEngine() (*knobs.Engine, error) {
	e, _ := knobs.New("keel") // a valid application name: New cannot fail
	if err := e.Attach(knobs.Module{Schema: SchemaYAML}); err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	return e, nil
}

// resolve merges every layer of e and writes each resolved value into cfg.
func resolve(e *knobs.Engine, cfg *Config) error {
	c, err := e.Resolve()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	for _, v := range c.Values() {
		if err := decodeValue(v, cfg); err != nil {
			return fmt.Errorf("config: %s: %w", v.Key, err)
		}
	}
	return nil
}

// decodeValue writes one knobs value into cfg through its yaml tags, so the
// field converts as it does from keel.yaml (a Duration from "5s").
func decodeValue(v knobs.Value, cfg *Config) error {
	path := strings.Split(v.Key, ".")[1:] // drop the application root
	var node interface{} = v.Value
	for i := len(path) - 1; i >= 0; i-- {
		node = map[string]interface{}{path[i]: node}
	}
	b, _ := yaml.Marshal(node) // nested maps of scalars and lists always marshal
	return yaml.Unmarshal(b, cfg)
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func AddrFromPort(port int) string { return ":" + strconv.Itoa(port) }
