// Package config loads the bootstrap configuration from environment variables.
//
// Only settings needed before the database is available live here. Everything
// else (indexers, Usenet servers, media servers, profiles) is configured in the
// web UI and stored in the database.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// EnvPrefix is prepended to every environment variable name.
const EnvPrefix = "AUTODOWNLOADARR_"

// Config is the bootstrap configuration.
type Config struct {
	PublicURL string
	// ListenAddr is the HTTP listen address, e.g. ":8686".
	ListenAddr string
	// ConfigDir holds the database and other persistent state.
	ConfigDir string
	// LogLevel is the minimum level that is logged.
	LogLevel slog.Level
	// LogFormat is "text" or "json".
	LogFormat string
	// TrustProxy makes the server honour X-Forwarded-For and X-Forwarded-Proto.
	// Enable it only when the app is reachable exclusively through a reverse proxy.
	TrustProxy bool
	// SessionTTL is how long a login stays valid without activity.
	SessionTTL time.Duration
	// TMDBURL overrides the TMDB API endpoint (for proxies and tests).
	TMDBURL string
}

// Default returns the configuration used when no environment variables are set.
func Default() Config {
	return Config{
		ListenAddr: ":8686",
		ConfigDir:  "/config",
		LogLevel:   slog.LevelInfo,
		LogFormat:  "text",
		SessionTTL: 30 * 24 * time.Hour,
	}
}

// DatabasePath is the location of the SQLite database.
func (c Config) DatabasePath() string {
	return filepath.Join(c.ConfigDir, "autodownloadarr.db")
}

// Load reads the configuration through getenv (usually os.Getenv). It reports
// all invalid values at once.
func Load(getenv func(string) string) (Config, error) {
	c := Default()
	var errs []error
	get := func(name string) string { return getenv(EnvPrefix + name) }

	if v := get("LISTEN"); v != "" {
		c.ListenAddr = v
	}
	if v := get("CONFIG_DIR"); v != "" {
		c.ConfigDir = v
	}
	if v := get("LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("%sLOG_LEVEL: %w", EnvPrefix, err))
		}
	}
	if v := get("LOG_FORMAT"); v != "" {
		if v != "text" && v != "json" {
			errs = append(errs, fmt.Errorf("%sLOG_FORMAT: must be \"text\" or \"json\", got %q", EnvPrefix, v))
		}
		c.LogFormat = v
	}
	if v := get("TRUST_PROXY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%sTRUST_PROXY: %w", EnvPrefix, err))
		}
		c.TrustProxy = b
	}
	c.TMDBURL = get("TMDB_URL")
	c.PublicURL = strings.TrimRight(get("PUBLIC_URL"), "/")
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		valid := err == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == ""
		if valid {
			loopback := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
			valid = u.Scheme == "https" || (u.Scheme == "http" && loopback)
		}
		if !valid {
			errs = append(errs, fmt.Errorf("%sPUBLIC_URL: must be an HTTPS origin (HTTP allowed for localhost)", EnvPrefix))
		}
	}
	if v := get("SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%sSESSION_TTL: %w", EnvPrefix, err))
		case d < time.Hour:
			errs = append(errs, fmt.Errorf("%sSESSION_TTL: must be at least 1h, got %s", EnvPrefix, d))
		default:
			c.SessionTTL = d
		}
	}

	return c, errors.Join(errs...)
}
