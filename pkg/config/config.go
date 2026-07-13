package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/viper"
)

// ServerConfig holds server configuration.
type ServerConfig struct {
	Listen         string `mapstructure:"listen"`
	TLSCert        string `mapstructure:"tls_cert"`
	TLSKey         string `mapstructure:"tls_key"`
	AuthorizedKeys string `mapstructure:"authorized_keys"`
	IPPool         string `mapstructure:"ip_pool"`
	Mode           string `mapstructure:"mode"`
	LANInterface   string `mapstructure:"lan_interface"`
	SocksListen    string `mapstructure:"socks_listen"`
	SocksTunnel    string `mapstructure:"socks_tunnel"`
	NoAuth         bool   `mapstructure:"no_auth"`
	Verbose        bool
}

func (c *ServerConfig) LogLevel() slog.Level {
	if c.Verbose {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// ClientConfig holds client configuration.
type ClientConfig struct {
	Server            string        `mapstructure:"server"`
	Name              string        `mapstructure:"name"`
	ExposePorts       []int         `mapstructure:"expose"`
	ExposeSubnets     []string      `mapstructure:"expose_subnets"`
	Reconnect         bool          `mapstructure:"reconnect"`
	ReconnectInterval time.Duration `mapstructure:"reconnect_interval"`
	SSHKey            string        `mapstructure:"ssh_key"`
	Insecure          bool          `mapstructure:"insecure"`
	NoAuth            bool          `mapstructure:"no_auth"`
	TUN               bool          `mapstructure:"tun"`
	Verbose           bool
}

func (c *ClientConfig) LogLevel() slog.Level {
	if c.Verbose {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// RelayConfig holds relay configuration.
type RelayConfig struct {
	Listen  string `mapstructure:"listen"`
	TLSCert string `mapstructure:"tls_cert"`
	TLSKey  string `mapstructure:"tls_key"`
	Verbose bool
}

func (c *RelayConfig) LogLevel() slog.Level {
	if c.Verbose {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// LoadServer loads server configuration from viper.
func LoadServer() (*ServerConfig, error) {
	cfg := &ServerConfig{
		Listen:         viper.GetString("server.listen"),
		TLSCert:        viper.GetString("server.tls.cert"),
		TLSKey:         viper.GetString("server.tls.key"),
		AuthorizedKeys: viper.GetString("server.authorized_keys"),
		IPPool:         viper.GetString("server.ip_pool"),
		Mode:           viper.GetString("server.mode"),
		LANInterface:   viper.GetString("server.lan_interface"),
		SocksListen:    viper.GetString("server.socks_listen"),
		SocksTunnel:    viper.GetString("server.socks_tunnel"),
		NoAuth:         viper.GetBool("server.no_auth"),
		Verbose:        viper.GetBool("verbose"),
	}

	if cfg.Listen == "" {
		cfg.Listen = ":8443"
	}
	if cfg.AuthorizedKeys == "" {
		cfg.AuthorizedKeys = "~/.ssh/authorized_keys"
	}
	if cfg.IPPool == "" {
		cfg.IPPool = "10.99.0.0/16"
	}
	if cfg.Mode == "" {
		cfg.Mode = "tun"
	}
	if cfg.Mode != "tun" && cfg.Mode != "socks" {
		return nil, fmt.Errorf("invalid mode %q: must be 'tun' or 'socks'", cfg.Mode)
	}

	return cfg, nil
}

// LoadClient loads client configuration from viper.
func LoadClient() (*ClientConfig, error) {
	cfg := &ClientConfig{
		Server:            viper.GetString("client.server"),
		Name:              viper.GetString("client.name"),
		ExposePorts:       viper.GetIntSlice("client.expose"),
		ExposeSubnets:     viper.GetStringSlice("client.expose_subnets"),
		Reconnect:         viper.GetBool("client.reconnect"),
		ReconnectInterval: viper.GetDuration("client.reconnect_interval"),
		SSHKey:            viper.GetString("client.ssh_key"),
		Insecure:          viper.GetBool("client.insecure"),
		NoAuth:            viper.GetBool("client.no_auth"),
		TUN:               viper.GetBool("client.tun"),
		Verbose:           viper.GetBool("verbose"),
	}

	if cfg.Server == "" {
		return nil, fmt.Errorf("server address is required")
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("tunnel name is required")
	}
	if cfg.ReconnectInterval == 0 {
		cfg.ReconnectInterval = 5 * time.Second
	}
	if len(cfg.ExposePorts) == 0 {
		cfg.ExposePorts = []int{22}
	}

	return cfg, nil
}

// LoadRelay loads relay configuration from viper.
func LoadRelay() (*RelayConfig, error) {
	cfg := &RelayConfig{
		Listen:  viper.GetString("relay.listen"),
		TLSCert: viper.GetString("relay.tls.cert"),
		TLSKey:  viper.GetString("relay.tls.key"),
		Verbose: viper.GetBool("verbose"),
	}

	if cfg.Listen == "" {
		cfg.Listen = ":8443"
	}

	return cfg, nil
}
