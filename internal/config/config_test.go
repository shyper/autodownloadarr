package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c != Default() {
		t.Errorf("got %+v, want defaults %+v", c, Default())
	}
	if got, want := c.DatabasePath(), "/config/autodownloadarr.db"; got != want {
		t.Errorf("DatabasePath = %q, want %q", got, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"AUTODOWNLOADARR_LISTEN":      "127.0.0.1:9000",
		"AUTODOWNLOADARR_CONFIG_DIR":  "/tmp/ada",
		"AUTODOWNLOADARR_LOG_LEVEL":   "debug",
		"AUTODOWNLOADARR_LOG_FORMAT":  "json",
		"AUTODOWNLOADARR_TRUST_PROXY": "true",
		"AUTODOWNLOADARR_SESSION_TTL": "48h",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		ListenAddr: "127.0.0.1:9000",
		ConfigDir:  "/tmp/ada",
		LogLevel:   slog.LevelDebug,
		LogFormat:  "json",
		TrustProxy: true,
		SessionTTL: 48 * time.Hour,
	}
	if c != want {
		t.Errorf("got %+v, want %+v", c, want)
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := Load(env(map[string]string{
		"AUTODOWNLOADARR_LOG_LEVEL":   "loud",
		"AUTODOWNLOADARR_LOG_FORMAT":  "xml",
		"AUTODOWNLOADARR_TRUST_PROXY": "maybe",
		"AUTODOWNLOADARR_SESSION_TTL": "5m",
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, name := range []string{"LOG_LEVEL", "LOG_FORMAT", "TRUST_PROXY", "SESSION_TTL"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not mention %s: %v", name, err)
		}
	}
}

func TestPublicURLRequiresSecureOrigin(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"https://media.example", true},
		{"https://media.example/", true},
		{"http://localhost:8686", true},
		{"http://127.0.0.1:8686", true},
		{"http://[::1]:8686", true},
		{"http://media.example", false},
		{"https://user:password@media.example", false},
		{"https://media.example/path", false},
		{"https://media.example?query=1", false},
		{"https://media.example#fragment", false},
		{"https://%invalid", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			_, err := Load(env(map[string]string{"AUTODOWNLOADARR_PUBLIC_URL": tc.url}))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}
