package config

import (
	"os"
	"path/filepath"
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
