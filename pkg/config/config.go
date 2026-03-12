// pkg/config/config.go
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"gopkg.in/yaml.v3"

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
	CertFile string     `yaml:"cert_file"`
	KeyFile  string     `yaml:"key_file"`
	ACME     ACMEConfig `yaml:"acme"`
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

type HeaderPolicyConfig struct {
	Forward []string `yaml:"forward"`
	Strip   []string `yaml:"strip"`
}

type SidecarConfig struct {
	Enabled                bool                 `yaml:"enabled"`
	UpstreamURL            string               `yaml:"upstream_url"`
	UpstreamHealthPath     string               `yaml:"upstream_health_path"`
	UpstreamHealthInterval Duration             `yaml:"upstream_health_interval"`
	UpstreamHealthTimeout  Duration             `yaml:"upstream_health_timeout"`
	UpstreamTLS            UpstreamTLSConfig    `yaml:"upstream_tls"`
	CircuitBreaker         CircuitBreakerConfig `yaml:"circuit_breaker"`
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
}

type AuthnConfig struct {
	Enabled            bool     `yaml:"enabled"`
	TrustedIDs         []string `yaml:"trusted_ids"`
	TrustedSigners     []string `yaml:"trusted_signers"`
	TrustedSignersFile string   `yaml:"trusted_signers_file"`
	MyID               string   `yaml:"my_id"`
	MySignatureKeyFile string   `yaml:"my_signature_key_file"`
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
	Metrics      MetricsConfig      `yaml:"metrics"`
	Tracing      TracingConfig      `yaml:"tracing"`
	FIPS         FIPSConfig         `yaml:"fips"`
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
			HighWatermark:   0.85,
			LowWatermark:    0.70,
			SheddingEnabled: true,
		},
		Authn: AuthnConfig{
			Enabled: true,
		},
		ExtAuthz: ExtAuthzConfig{
			Timeout:   DurationOf(500 * time.Millisecond),
			Transport: "http",
		},
		Logging: LoggingConfig{
			JSON:      true,
			Level:     "info",
			AccessLog: true,
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
			XFFMode: "append",
		},
	}
}

// ---------------------------------------------------------------------------
// Load — merge order: defaults → config YAML → secrets YAML → ENV vars.
// ---------------------------------------------------------------------------

// Default loads config using the standard pipeline:
// defaults → $KEEL_CONFIG file → $KEEL_SECRETS file → env vars → validate.
// On error it calls log.Fatal; it never returns an error.
func Default(log *logging.Logger) Config {
	cfg, err := Load(os.Getenv("KEEL_CONFIG"), os.Getenv("KEEL_SECRETS"))
	if err != nil {
		log.Fatal("config_load_failed", map[string]any{"err": err.Error()})
	}
	return cfg
}

// Load runs the merge pipeline (defaults → configPath file → secretsPath file →
// env vars → validate) and returns any error to the caller.
// Either path may be empty. Use Default for CLI entry points.
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

// From applies environment variable overrides onto c, validates the result,
// and returns it. Intended for library users who supply their own Config.
func From(c *Config) (Config, error) {
	cfg := *c
	applyEnv(&cfg)
	if err := Validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// load is the internal merge pipeline: defaults → config YAML → secrets YAML → env vars.
func load(configPath, secretsPath string) (Config, error) {
	cfg := defaults()

	if configPath != "" {
		if err := applyYAMLFile(configPath, &cfg); err != nil {
			return cfg, fmt.Errorf("config file: %w", err)
		}
	}

	if secretsPath != "" {
		if err := applyYAMLFile(secretsPath, &cfg); err != nil {
			return cfg, fmt.Errorf("secrets file: %w", err)
		}
	}

	applyEnv(&cfg)

	return cfg, nil
}

// Validate returns an error if the config contains an invalid combination
// or violates the constraints declared in pkg/config/schema.yaml.
func Validate(cfg Config) error {
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
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func applyYAMLFile(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if err := validateAgainstSchema(b); err != nil {
		return fmt.Errorf("schema %q: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if err == io.EOF {
			return nil // empty file is valid: no overrides
		}
		return fmt.Errorf("parse %q: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// JSON Schema validation — compiled once from the embedded schema.yaml.
// ---------------------------------------------------------------------------

var (
	compiledSchema     *jsonschema.Schema
	compiledSchemaErr  error
	compiledSchemaOnce sync.Once
)

func getSchema() (*jsonschema.Schema, error) {
	compiledSchemaOnce.Do(func() {
		var raw interface{}
		if err := yaml.Unmarshal(SchemaYAML, &raw); err != nil {
			compiledSchemaErr = fmt.Errorf("parse embedded schema: %w", err)
			return
		}
		b, err := json.Marshal(raw)
		if err != nil {
			compiledSchemaErr = fmt.Errorf("convert schema to JSON: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("keel:config", bytes.NewReader(b)); err != nil {
			compiledSchemaErr = fmt.Errorf("load schema: %w", err)
			return
		}
		compiledSchema, compiledSchemaErr = c.Compile("keel:config")
	})
	return compiledSchema, compiledSchemaErr
}

// validateAgainstSchema validates raw YAML bytes against the embedded JSON
// Schema.  Empty or whitespace-only input is accepted (no overrides).
func validateAgainstSchema(b []byte) error {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	sc, err := getSchema()
	if err != nil {
		return err
	}
	var doc interface{}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	if doc == nil {
		return nil
	}
	return sc.Validate(doc)
}

// applyEnv overlays environment variables onto cfg. Only vars that are
// explicitly set (non-empty) override the current value.
func applyEnv(cfg *Config) {
	// Listeners
	applyBool("KEEL_HTTP_ENABLED", &cfg.Listeners.HTTP.Enabled)
	applyInt("KEEL_HTTP_PORT", &cfg.Listeners.HTTP.Port)
	applyBool("KEEL_HTTPS_ENABLED", &cfg.Listeners.HTTPS.Enabled)
	applyInt("KEEL_HTTPS_PORT", &cfg.Listeners.HTTPS.Port)
	applyBool("KEEL_H3_ENABLED", &cfg.Listeners.H3.Enabled)
	applyInt("KEEL_H3_PORT", &cfg.Listeners.H3.Port)
	applyBool("KEEL_HEALTH_ENABLED", &cfg.Listeners.Health.Enabled)
	applyInt("KEEL_HEALTH_PORT", &cfg.Listeners.Health.Port)
	applyBool("KEEL_READY_ENABLED", &cfg.Listeners.Ready.Enabled)
	applyInt("KEEL_READY_PORT", &cfg.Listeners.Ready.Port)
	applyBool("KEEL_STARTUP_ENABLED", &cfg.Listeners.Startup.Enabled)
	applyInt("KEEL_STARTUP_PORT", &cfg.Listeners.Startup.Port)
	applyBool("KEEL_ADMIN_ENABLED", &cfg.Listeners.Admin.Enabled)
	applyInt("KEEL_ADMIN_PORT", &cfg.Listeners.Admin.Port)

	// TLS
	applyString("KEEL_TLS_CERT", &cfg.TLS.CertFile)
	applyString("KEEL_TLS_KEY", &cfg.TLS.KeyFile)

	// Sidecar
	applyBool("KEEL_SIDECAR", &cfg.Sidecar.Enabled)
	applyString("KEEL_UPSTREAM_URL", &cfg.Sidecar.UpstreamURL)
	applyBool("KEEL_UPSTREAM_TLS", &cfg.Sidecar.UpstreamTLS.Enabled)
	applyString("KEEL_UPSTREAM_CA_FILE", &cfg.Sidecar.UpstreamTLS.CAFile)
	applyString("KEEL_UPSTREAM_CLIENT_CERT", &cfg.Sidecar.UpstreamTLS.ClientCertFile)
	applyString("KEEL_UPSTREAM_CLIENT_KEY", &cfg.Sidecar.UpstreamTLS.ClientKeyFile)

	// Security
	applyBool("KEEL_OWASP", &cfg.Security.OWASPHeaders)
	applyInt64("KEEL_MAX_REQ_BODY_BYTES", &cfg.Security.MaxRequestBodyBytes)
	applyInt64("KEEL_MAX_RESP_BODY_BYTES", &cfg.Security.MaxResponseBodyBytes)
	applyInt("KEEL_MAX_HEADER_BYTES", &cfg.Security.MaxHeaderBytes)

	// Timeouts
	applyDuration("KEEL_READ_HEADER_TIMEOUT", &cfg.Timeouts.ReadHeader)
	applyDuration("KEEL_READ_TIMEOUT", &cfg.Timeouts.Read)
	applyDuration("KEEL_WRITE_TIMEOUT", &cfg.Timeouts.Write)
	applyDuration("KEEL_IDLE_TIMEOUT", &cfg.Timeouts.Idle)
	applyDuration("KEEL_SHUTDOWN_DRAIN", &cfg.Timeouts.ShutdownDrain)
	applyDuration("KEEL_PRESTOP_SLEEP", &cfg.Timeouts.PrestopSleep)

	// Limits
	applyInt("KEEL_MAX_CONCURRENT", &cfg.Limits.MaxConcurrent)
	applyInt("KEEL_QUEUE_DEPTH", &cfg.Limits.QueueDepth)

	// Backpressure
	applyInt64("KEEL_HEAP_MAX_BYTES", &cfg.Backpressure.HeapMaxBytes)
	applyFloat64("KEEL_PRESSURE_HIGH", &cfg.Backpressure.HighWatermark)
	applyFloat64("KEEL_PRESSURE_LOW", &cfg.Backpressure.LowWatermark)
	applyBool("KEEL_SHEDDING", &cfg.Backpressure.SheddingEnabled)

	// Authn
	applyBool("KEEL_AUTHN", &cfg.Authn.Enabled)
	applyString("KEEL_MY_ID", &cfg.Authn.MyID)
	applyCSV("KEEL_TRUSTED_IDS", &cfg.Authn.TrustedIDs)
	applyCSV("KEEL_TRUSTED_SIGNERS", &cfg.Authn.TrustedSigners)

	// ExtAuthz
	applyBool("KEEL_AUTHZ", &cfg.ExtAuthz.Enabled)
	applyString("KEEL_AUTHZ_ENDPOINT", &cfg.ExtAuthz.Endpoint)
	applyString("KEEL_AUTHZ_PATH", &cfg.ExtAuthz.Path)
	applyDuration("KEEL_AUTHZ_TIMEOUT", &cfg.ExtAuthz.Timeout)
	applyString("KEEL_AUTHZ_TRANSPORT", &cfg.ExtAuthz.Transport)
	applyBool("KEEL_AUTHZ_FAIL_OPEN", &cfg.ExtAuthz.FailOpen)

	// Logging
	applyBool("KEEL_LOG_JSON", &cfg.Logging.JSON)
	applyString("KEEL_LOG_LEVEL", &cfg.Logging.Level)
}

// ---------------------------------------------------------------------------
// ENV primitives
// ---------------------------------------------------------------------------

func applyBool(key string, dst *bool) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	b, err := strconv.ParseBool(v)
	if err == nil {
		*dst = b
	}
}

func applyInt(key string, dst *int) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	n, err := strconv.Atoi(v)
	if err == nil {
		*dst = n
	}
}

func applyInt64(key string, dst *int64) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err == nil {
		*dst = n
	}
}

func applyFloat64(key string, dst *float64) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	f, err := strconv.ParseFloat(v, 64)
	if err == nil {
		*dst = f
	}
}

func applyString(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func applyDuration(key string, dst *Duration) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	d, err := time.ParseDuration(v)
	if err == nil {
		*dst = Duration{d}
	}
}

func applyCSV(key string, dst *[]string) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) > 0 {
		*dst = out
	}
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func AddrFromPort(port int) string { return ":" + strconv.Itoa(port) }
