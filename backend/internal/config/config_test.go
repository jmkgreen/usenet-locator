package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Database:  DatabaseConfig{URLFile: "database-url", MaxConns: 4},
		Accounts:  []AccountConfig{{ID: "primary", UsernameFile: "nntp-user", PasswordFile: "nntp-password", ConnectionLimit: 1}},
		Endpoints: []EndpointConfig{{ID: "primary-eu", AccountID: "primary", Host: "news.example.test", Port: 563, TLS: true, Primary: true}},
		Resources: ResourceConfig{ActiveJobs: 1, WorkersPerJob: 1, BatchSize: 250, MaxBodyBytes: 5_000_000},
	}
}

func TestValidateAcceptsSafeConfiguration(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsPlaintextWithoutAcknowledgement(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].TLS = false
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() succeeded for unacknowledged plaintext endpoint")
	}
}

func TestValidateRejectsUnknownAccount(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].AccountID = "missing"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() succeeded for unknown account")
	}
}

func TestValidateRequiresOnePrimaryEndpoint(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].Primary = false
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() succeeded without a primary endpoint")
	}
}

func TestValidateAllowsOnlyKnownOverviewCommands(t *testing.T) {
	cfg := validConfig()
	cfg.Endpoints[0].OverviewCommand = "xover"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() xover error = %v", err)
	}
	cfg.Endpoints[0].OverviewCommand = "unsupported"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown overview command")
	}
}

func TestValidateRejectsAdditionalInvalidLimitsAndIdentifiers(t *testing.T) {
	limit := int64(0)
	disabled := false
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"database connection limit", func(cfg *Config) { cfg.Database.MaxConns = 33 }},
		{"non-positive resource limit", func(cfg *Config) { cfg.Resources.ActiveJobs = 0 }},
		{"oversized batch", func(cfg *Config) { cfg.Resources.BatchSize = 1001 }},
		{"duplicate account", func(cfg *Config) { cfg.Accounts = append(cfg.Accounts, cfg.Accounts[0]) }},
		{"zero transfer limit", func(cfg *Config) { cfg.Accounts[0].TransferLimitBytes = &limit }},
		{"invalid endpoint port", func(cfg *Config) { cfg.Endpoints[0].Port = 0 }},
		{"duplicate endpoint", func(cfg *Config) { cfg.Endpoints = append(cfg.Endpoints, cfg.Endpoints[0]) }},
		{"disabled primary", func(cfg *Config) { cfg.Endpoints[0].Enabled = &disabled }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestReadSecretFileTrimsMountedSecretNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := ReadSecretFile(path)
	if err != nil || value != "secret-value" {
		t.Fatalf("ReadSecretFile() = %q, %v", value, err)
	}
}

func TestReadSecretFileRejectsEmptySecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecretFile(path); err == nil {
		t.Fatal("ReadSecretFile() succeeded for empty secret")
	}
}

func TestLoadRejectsUnknownFieldsAndNeverAcceptsUnsafeEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{"database":{"url_file":"database-url","max_conns":4},"accounts":[{"id":"primary","username_file":"user","password_file":"password","connection_limit":1}],"endpoints":[{"id":"primary","account_id":"primary","host":"news.example","port":563,"tls":false,"primary":true}],"resources":{"active_jobs":1,"workers_per_job":1,"batch_size":1,"max_body_bytes":1},"unexpected":true}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("Load() error = %v", err)
	}
	contents = strings.Replace(contents, `,"unexpected":true`, "", 1)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "plaintext acknowledgement") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestCredentialsDoNotReturnPartialSecretOnFailure(t *testing.T) {
	dir := t.TempDir()
	usernameFile := filepath.Join(dir, "username")
	if err := os.WriteFile(usernameFile, []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	username, password, err := (AccountConfig{UsernameFile: usernameFile, PasswordFile: filepath.Join(dir, "missing")}).Credentials()
	if err == nil || username != "" || password != "" || strings.Contains(err.Error(), "operator") {
		t.Fatalf("Credentials() = %q, %q, %v", username, password, err)
	}
}
