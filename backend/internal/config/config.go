// Package config loads non-secret application configuration and validates the
// safety-critical invariants before the service accepts work.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Database  DatabaseConfig   `json:"database"`
	Accounts  []AccountConfig  `json:"accounts"`
	Endpoints []EndpointConfig `json:"endpoints"`
	Resources ResourceConfig   `json:"resources"`
}

type DatabaseConfig struct {
	URLFromEnv string `json:"url_from_env"`
	MaxConns   int32  `json:"max_conns"`
}

type AccountConfig struct {
	ID                 string `json:"id"`
	UsernameFromEnv    string `json:"username_from_env"`
	PasswordFromEnv    string `json:"password_from_env"`
	ConnectionLimit    int    `json:"connection_limit"`
	TransferLimitBytes *int64 `json:"transfer_limit_bytes,omitempty"`
}

type EndpointConfig struct {
	ID                    string `json:"id"`
	AccountID             string `json:"account_id"`
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	TLS                   bool   `json:"tls"`
	PlaintextAcknowledged bool   `json:"plaintext_acknowledged"`
	Primary               bool   `json:"primary"`
	Priority              int    `json:"priority"`
	OverviewCommand       string `json:"overview_command,omitempty"`
}

type ResourceConfig struct {
	ActiveJobs    int `json:"active_jobs"`
	WorkersPerJob int `json:"workers_per_job"`
	BatchSize     int `json:"batch_size"`
	MaxBodyBytes  int `json:"max_body_bytes"`
}

func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := requireEnvReference("database.url_from_env", c.Database.URLFromEnv); err != nil {
		return err
	}
	if c.Database.MaxConns < 1 || c.Database.MaxConns > 32 {
		return fmt.Errorf("database.max_conns must be between 1 and 32")
	}
	if c.Resources.ActiveJobs < 1 || c.Resources.WorkersPerJob < 1 || c.Resources.BatchSize < 1 || c.Resources.MaxBodyBytes < 1 {
		return fmt.Errorf("resource limits must all be positive")
	}
	if c.Resources.BatchSize > 1000 {
		return fmt.Errorf("resources.batch_size must not exceed 1000")
	}

	accounts := make(map[string]struct{}, len(c.Accounts))
	for _, account := range c.Accounts {
		if account.ID == "" || account.ConnectionLimit < 1 {
			return fmt.Errorf("account id and connection_limit are required")
		}
		if _, exists := accounts[account.ID]; exists {
			return fmt.Errorf("duplicate account id %q", account.ID)
		}
		if err := requireEnvReference("account.username_from_env", account.UsernameFromEnv); err != nil {
			return err
		}
		if err := requireEnvReference("account.password_from_env", account.PasswordFromEnv); err != nil {
			return err
		}
		if account.TransferLimitBytes != nil && *account.TransferLimitBytes < 1 {
			return fmt.Errorf("account %q transfer_limit_bytes must be positive", account.ID)
		}
		accounts[account.ID] = struct{}{}
	}

	endpointIDs := make(map[string]struct{}, len(c.Endpoints))
	primaryCount := 0
	for _, endpoint := range c.Endpoints {
		if endpoint.ID == "" || endpoint.Host == "" || endpoint.Port < 1 || endpoint.Port > 65535 {
			return fmt.Errorf("endpoint id, host, and valid port are required")
		}
		if _, exists := accounts[endpoint.AccountID]; !exists {
			return fmt.Errorf("endpoint %q references unknown account %q", endpoint.ID, endpoint.AccountID)
		}
		if _, exists := endpointIDs[endpoint.ID]; exists {
			return fmt.Errorf("duplicate endpoint id %q", endpoint.ID)
		}
		if !endpoint.TLS && !endpoint.PlaintextAcknowledged {
			return fmt.Errorf("endpoint %q disables TLS without plaintext acknowledgement", endpoint.ID)
		}
		if endpoint.OverviewCommand != "" && endpoint.OverviewCommand != "auto" && endpoint.OverviewCommand != "over" && endpoint.OverviewCommand != "xover" {
			return fmt.Errorf("endpoint %q overview_command must be auto, over, or xover", endpoint.ID)
		}
		if endpoint.Primary {
			primaryCount++
		}
		endpointIDs[endpoint.ID] = struct{}{}
	}
	if primaryCount != 1 {
		return fmt.Errorf("exactly one primary endpoint is required")
	}
	return nil
}

func requireEnvReference(field, value string) error {
	if value == "" || !strings.HasPrefix(value, "USENET_LOCATOR_") {
		return fmt.Errorf("%s must be a USENET_LOCATOR_ environment-variable reference", field)
	}
	return nil
}
