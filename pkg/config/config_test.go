package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestLoadServer_Defaults(t *testing.T) {
	viper.Reset()
	cfg, err := LoadServer()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8443" {
		t.Errorf("expected default listen :8443, got %s", cfg.Listen)
	}
	if cfg.Mode != "tun" {
		t.Errorf("expected default mode tun, got %s", cfg.Mode)
	}
	if cfg.IPPool != "10.99.0.0/16" {
		t.Errorf("expected default pool 10.99.0.0/16, got %s", cfg.IPPool)
	}
}

func TestLoadServer_InvalidMode(t *testing.T) {
	viper.Reset()
	viper.Set("server.mode", "invalid")
	_, err := LoadServer()
	if err == nil {
		t.Error("expected error for invalid mode, got nil")
	}
}

func TestLoadServer_FromYAML(t *testing.T) {
	viper.Reset()
	viper.Set("server.listen", "192.168.1.100:8444")
	viper.Set("server.mode", "tun")
	viper.Set("server.ip_pool", "10.50.0.0/16")
	viper.Set("server.no_auth", true)

	cfg, err := LoadServer()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "192.168.1.100:8444" {
		t.Errorf("expected listen 192.168.1.100:8444, got %s", cfg.Listen)
	}
	if cfg.IPPool != "10.50.0.0/16" {
		t.Errorf("expected pool 10.50.0.0/16, got %s", cfg.IPPool)
	}
	if !cfg.NoAuth {
		t.Error("expected NoAuth true")
	}
}

func TestLoadClient_EmptyServerReturnsError(t *testing.T) {
	viper.Reset()
	_, err := LoadClient()
	if err == nil {
		t.Error("expected error for empty server, got nil")
	}
}

func TestLoadClient_FromYAML(t *testing.T) {
	viper.Reset()
	viper.Set("client.server", "ws://192.168.1.100:8444")
	viper.Set("client.name", "test-client")
	viper.Set("client.expose", []int{22, 1234})
	viper.Set("client.tun", true)
	viper.Set("client.no_auth", true)
	viper.Set("client.reconnect", true)

	cfg, err := LoadClient()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server != "ws://192.168.1.100:8444" {
		t.Errorf("expected server ws://192.168.1.100:8444, got %s", cfg.Server)
	}
	if cfg.Name != "test-client" {
		t.Errorf("expected name test-client, got %s", cfg.Name)
	}
	if len(cfg.ExposePorts) != 2 || cfg.ExposePorts[0] != 22 || cfg.ExposePorts[1] != 1234 {
		t.Errorf("expected expose [22 1234], got %v", cfg.ExposePorts)
	}
	if !cfg.TUN {
		t.Error("expected TUN true")
	}
	if !cfg.NoAuth {
		t.Error("expected NoAuth true")
	}
	if !cfg.Reconnect {
		t.Error("expected Reconnect true")
	}
}

func TestLoadClient_MissingServerDetected(t *testing.T) {
	viper.Reset()
	// Simulate a corrupt config: client section exists but server is empty
	viper.Set("client.name", "orphan-client")

	_, err := LoadClient()
	if err == nil {
		t.Error("expected error for missing server, got nil")
	}
}
