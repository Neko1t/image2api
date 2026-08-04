package config

import "testing"

func validAPIMediaConfig() *Config {
	return &Config{
		APIImagePersistence:       "off",
		APIMediaRetentionDays:     30,
		APIMediaMaxBytes:          1024 * 1024 * 1024,
		APIMediaIngestConcurrency: 2,
	}
}

func TestValidateAPIMediaConfig(t *testing.T) {
	if err := validateAPIMediaConfig(validAPIMediaConfig()); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"image mode", func(c *Config) { c.APIImagePersistence = "sometimes" }},
		{"retention", func(c *Config) { c.APIMediaRetentionDays = 0 }},
		{"max bytes", func(c *Config) { c.APIMediaMaxBytes = 1024 }},
		{"concurrency", func(c *Config) { c.APIMediaIngestConcurrency = 17 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validAPIMediaConfig()
			tc.mutate(cfg)
			if err := validateAPIMediaConfig(cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
