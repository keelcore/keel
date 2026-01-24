// pkg/config/config.go
package config

import (
    "encoding/json"
    "fmt"
    "os"
    "strconv"
    "time"

    "github.com/keelcore/keel/pkg/core/ports"
)

type Listener struct {
    Enabled bool
    Port    int
}

type Config struct {
    HTTP  Listener
    HTTPS Listener
    H3    Listener

    Admin  Listener
    Health Listener
    Ready  Listener

    TLSCertFile string
    TLSKeyFile  string

    SidecarEnabled bool
    UpstreamURL    string

    SecurityHeadersEnabled bool
    MaxRequestBodyBytes    int64
    ReadHeaderTimeout      time.Duration
    ReadTimeout            time.Duration
    WriteTimeout           time.Duration
    IdleTimeout            time.Duration

    HeapMaxBytes          int64
    PressureHighWatermark float64
    PressureLowWatermark  float64
    SheddingEnabled       bool

    AuthnEnabled   bool
    TrustedIDs     []string
    TrustedSigners []string
    MyID           string

    LogJSON bool
}

func AddrFromPort(port int) string { return ":" + strconv.Itoa(port) }

func LoadFromEnvAndOptionalFile(path string) (Config, error) {
    cfg := Config{
        HTTP: Listener{
            Enabled: getenvBool("KEEL_HTTP_ENABLED", true),
            Port:    getenvInt("KEEL_HTTP_PORT", ports.HTTP),
        },
        HTTPS: Listener{
            Enabled: getenvBool("KEEL_HTTPS_ENABLED", false),
            Port:    getenvInt("KEEL_HTTPS_PORT", ports.HTTPS),
        },
        H3: Listener{
            Enabled: getenvBool("KEEL_H3_ENABLED", false),
            Port:    getenvInt("KEEL_H3_PORT", ports.H3),
        },
        Admin: Listener{
            Enabled: getenvBool("KEEL_ADMIN_ENABLED", false),
            Port:    getenvInt("KEEL_ADMIN_PORT", ports.ADMIN),
        },
        Health: Listener{
            Enabled: getenvBool("KEEL_HEALTH_ENABLED", false),
            Port:    getenvInt("KEEL_HEALTH_PORT", ports.HEALTH),
        },
        Ready: Listener{
            Enabled: getenvBool("KEEL_READY_ENABLED", false),
            Port:    getenvInt("KEEL_READY_PORT", ports.READY),
        },

        TLSCertFile: os.Getenv("KEEL_TLS_CERT"),
        TLSKeyFile:  os.Getenv("KEEL_TLS_KEY"),

        SidecarEnabled:         getenvBool("KEEL_SIDECAR", false),
        UpstreamURL:            os.Getenv("KEEL_UPSTREAM_URL"),
        SecurityHeadersEnabled: getenvBool("KEEL_OWASP", true),
        MaxRequestBodyBytes:    int64(getenvInt("KEEL_MAX_REQ_BODY_BYTES", 10<<20)),
        ReadHeaderTimeout:      getenvDuration("KEEL_READ_HEADER_TIMEOUT", 5*time.Second),
        ReadTimeout:            getenvDuration("KEEL_READ_TIMEOUT", 30*time.Second),
        WriteTimeout:           getenvDuration("KEEL_WRITE_TIMEOUT", 30*time.Second),
        IdleTimeout:            getenvDuration("KEEL_IDLE_TIMEOUT", 60*time.Second),

        HeapMaxBytes:          getenvInt64("KEEL_HEAP_MAX_BYTES", 0),
        PressureHighWatermark: getenvFloat("KEEL_PRESSURE_HIGH", 0.85),
        PressureLowWatermark:  getenvFloat("KEEL_PRESSURE_LOW", 0.70),
        SheddingEnabled:       getenvBool("KEEL_SHEDDING", true),

        AuthnEnabled: getenvBool("KEEL_AUTHN", true),
        MyID:         os.Getenv("KEEL_MY_ID"),

        LogJSON: getenvBool("KEEL_LOG_JSON", true),
    }

    if path == "" {
        return cfg, nil
    }
    b, err := os.ReadFile(path)
    if err != nil {
        return cfg, fmt.Errorf("read config file: %w", err)
    }
    var fromFile Config
    if err := json.Unmarshal(b, &fromFile); err != nil {
        return cfg, fmt.Errorf("parse config file json: %w", err)
    }

    // Minimal merge: file overrides env-derived defaults when fields are non-zero.
    mergeConfig(&cfg, fromFile)
    return cfg, nil
}

func mergeListener(dst *Listener, src Listener) {
    if src.Port != 0 {
        dst.Port = src.Port
    }
    dst.Enabled = src.Enabled
}

func mergeConfig(dst *Config, src Config) {
    mergeListener(&dst.HTTP, src.HTTP)
    mergeListener(&dst.HTTPS, src.HTTPS)
    mergeListener(&dst.H3, src.H3)
    mergeListener(&dst.Admin, src.Admin)
    mergeListener(&dst.Health, src.Health)
    mergeListener(&dst.Ready, src.Ready)

    if src.TLSCertFile != "" {
        dst.TLSCertFile = src.TLSCertFile
    }
    if src.TLSKeyFile != "" {
        dst.TLSKeyFile = src.TLSKeyFile
    }
    if src.UpstreamURL != "" {
        dst.UpstreamURL = src.UpstreamURL
    }

    dst.SidecarEnabled = src.SidecarEnabled
    dst.SecurityHeadersEnabled = src.SecurityHeadersEnabled
    dst.SheddingEnabled = src.SheddingEnabled
    dst.AuthnEnabled = src.AuthnEnabled
    dst.LogJSON = src.LogJSON

    if src.MaxRequestBodyBytes != 0 {
        dst.MaxRequestBodyBytes = src.MaxRequestBodyBytes
    }
    if src.ReadHeaderTimeout != 0 {
        dst.ReadHeaderTimeout = src.ReadHeaderTimeout
    }
    if src.ReadTimeout != 0 {
        dst.ReadTimeout = src.ReadTimeout
    }
    if src.WriteTimeout != 0 {
        dst.WriteTimeout = src.WriteTimeout
    }
    if src.IdleTimeout != 0 {
        dst.IdleTimeout = src.IdleTimeout
    }
    if src.HeapMaxBytes != 0 {
        dst.HeapMaxBytes = src.HeapMaxBytes
    }
    if src.PressureHighWatermark != 0 {
        dst.PressureHighWatermark = src.PressureHighWatermark
    }
    if src.PressureLowWatermark != 0 {
        dst.PressureLowWatermark = src.PressureLowWatermark
    }

    if len(src.TrustedIDs) > 0 {
        dst.TrustedIDs = append([]string(nil), src.TrustedIDs...)
    }
    if len(src.TrustedSigners) > 0 {
        dst.TrustedSigners = append([]string(nil), src.TrustedSigners...)
    }
    if src.MyID != "" {
        dst.MyID = src.MyID
    }
}

func getenvInt(k string, def int) int {
    v := os.Getenv(k)
    if v == "" {
        return def
    }
    n, err := strconv.Atoi(v)
    if err != nil {
        return def
    }
    return n
}

func getenvInt64(k string, def int64) int64 {
    v := os.Getenv(k)
    if v == "" {
        return def
    }
    n, err := strconv.ParseInt(v, 10, 64)
    if err != nil {
        return def
    }
    return n
}

func getenvFloat(k string, def float64) float64 {
    v := os.Getenv(k)
    if v == "" {
        return def
    }
    f, err := strconv.ParseFloat(v, 64)
    if err != nil {
        return def
    }
    return f
}

func getenvBool(k string, def bool) bool {
    v := os.Getenv(k)
    if v == "" {
        return def
    }
    b, err := strconv.ParseBool(v)
    if err != nil {
        return def
    }
    return b
}

func getenvDuration(k string, def time.Duration) time.Duration {
    v := os.Getenv(k)
    if v == "" {
        return def
    }
    d, err := time.ParseDuration(v)
    if err != nil {
        return def
    }
    return d
}
