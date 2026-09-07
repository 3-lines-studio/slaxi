package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// botConfig is the bot.toml definition read by the slaxi transport consumer.
// The standard root keys are reserved; unknown keys and tables are ignored.
type botConfig struct {
	Model   string     `toml:"model"`
	BaseURL string     `toml:"base_url"`
	Tools   []string   `toml:"tools"`
	Slack   slackTable `toml:"slack"`
}

type slackTable struct {
	MentionOnly bool `toml:"mention_only"`
}

// loadBotConfig reads Root/bot.toml if present. A missing file is an empty
// config; an unreadable or invalid file is an error.
func loadBotConfig(root string) (botConfig, error) {
	data, err := os.ReadFile(filepath.Join(root, "bot.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return botConfig{}, nil
		}
		return botConfig{}, fmt.Errorf("read bot.toml: %w", err)
	}
	var bc botConfig
	if err := toml.Unmarshal(data, &bc); err != nil {
		return botConfig{}, fmt.Errorf("parse bot.toml: %w", err)
	}
	return bc, nil
}

// readSecret returns the host-provided secret for name, preferring the env
// variable envName over the Botdir secrets/<fileName> file. A missing secret
// yields "" so callers can decide whether that is fatal.
func readSecret(root, envName, fileName string) (string, error) {
	if v := os.Getenv(envName); v != "" {
		return v, nil
	}
	data, err := os.ReadFile(filepath.Join(root, "secrets", fileName))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read secret %s: %w", fileName, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
