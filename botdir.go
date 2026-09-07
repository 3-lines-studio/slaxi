package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type botConfig struct {
	Model   string     `toml:"model"`
	BaseURL string     `toml:"base_url"`
	Tools   []string   `toml:"tools"`
	Slack   slackTable `toml:"slack"`
}

type slackTable struct {
	MentionOnly bool `toml:"mention_only"`
}

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
