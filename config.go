package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the relay and upstream SMTP configuration
type tConfig struct {
	Log              string        `yaml:"log"`
	LogLevel         string        `yaml:"log_level"`
	ListenAddr       string        `yaml:"listen_addr"`
	OAuth2Config     tOAuth2Config `yaml:"oauth2_config"`
	FallbackSMTPuser string        `yaml:"fallback_smtp_user"`
	FallbackSMTPpass string        `yaml:"fallback_smtp_pass"`
	SaveToSent       bool          `yaml:"save_to_sent"`

	// Stability configuration (all have sensible defaults)
	MaxMessageSize    int64  `yaml:"max_message_size"`    // Max email size in bytes (default 25MB)
	MaxConnections    int    `yaml:"max_connections"`     // Max concurrent connections (default 100)
	ConnectionTimeout int    `yaml:"connection_timeout"`  // Connection timeout in seconds (default 300)
	StrictAttachments bool   `yaml:"strict_attachments"`  // Fail on attachment decode error (default false)
	RetryAttempts     int    `yaml:"retry_attempts"`      // Graph API retry attempts (default 3)
	RetryInitialDelay int `yaml:"retry_initial_delay"` // Initial retry delay in ms (default 500)
}

// OAuth2Config holds OAuth2 client configuration
type tOAuth2Config struct {
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	TenantID     string   `yaml:"tenant_id"`
	Scopes       []string `yaml:"scopes"`
}

func loadConfig() error {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), "config.yaml"))
	if err != nil {
		return err
	}
	config = &tConfig{} // Allocate the struct before unmarshalling
	err = yaml.Unmarshal(data, config)
	if err != nil {
		return err
	}
	decryptConfigStrings()

	// Set sensible defaults for stability configuration
	if config.MaxMessageSize == 0 {
		config.MaxMessageSize = 25 * 1024 * 1024 // 25MB (Graph API limit)
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 100
	}
	if config.ConnectionTimeout == 0 {
		config.ConnectionTimeout = 300 // 5 minutes
	}
	if config.RetryAttempts < 1 {
		config.RetryAttempts = 3
	}
	if config.RetryInitialDelay == 0 {
		config.RetryInitialDelay = 500 // 500ms
	}
	return nil
}

func slogSetup() (err error) {
	if config.Log != "" {
		logPath := config.Log
		if filepath.Base(config.Log) == config.Log {
			logPath = filepath.Join(filepath.Dir(os.Args[0]), config.Log)
		}
		logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return err
		}
	} else {
		logFile = os.Stdout
	}
	if config.LogLevel == "" {
		config.LogLevel = "info"
	}
	var level slog.Level
	switch strings.ToLower(config.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: level,
	}))
	return nil
}
