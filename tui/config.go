package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultEndpoint = "https://o.5seg.top"

// Config is where the mirror lives and how to reach the collector.
//
// The token is read from OSHIETE_TOKEN first and otherwise from a file, so the
// TUI can be started without an environment dance once the file exists.
type Config struct {
	Endpoint string
	Token    string
	DBPath   string
}

func LoadConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("cannot locate home directory: %w", err)
	}
	dataDir := filepath.Join(home, "oshiete", "data")

	c := Config{
		Endpoint: strings.TrimRight(envOr("OSHIETE_ENDPOINT", defaultEndpoint), "/"),
		DBPath:   envOr("OSHIETE_DB", filepath.Join(dataDir, "mirror.db")),
	}

	tokenFile := envOr("OSHIETE_TOKEN_FILE", filepath.Join(dataDir, "token"))
	if t := strings.TrimSpace(os.Getenv("OSHIETE_TOKEN")); t != "" {
		c.Token = t
	} else {
		raw, err := os.ReadFile(tokenFile)
		if err != nil {
			return Config{}, fmt.Errorf("no sync token: set OSHIETE_TOKEN, or put it in %s", tokenFile)
		}
		c.Token = strings.TrimSpace(string(raw))
	}
	if c.Token == "" {
		return Config{}, fmt.Errorf("sync token is empty (checked OSHIETE_TOKEN and %s)", tokenFile)
	}

	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
