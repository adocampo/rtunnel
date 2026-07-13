package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/malevolent/rtunnel/pkg/config"
	"github.com/malevolent/rtunnel/pkg/transport"
	"github.com/malevolent/rtunnel/pkg/tun"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Run the rtunnel client (connects to a server from a private network)",
	Long: `The client runs on the private/isolated machine (WSL, Docker container,
remote VM). It initiates an outbound WebSocket connection to the rtunnel
server, allowing the server to route traffic to this machine.`,
	RunE: runClient,
}

func init() {
	rootCmd.AddCommand(clientCmd)
	clientCmd.Flags().StringP("server", "s", "", "server address (e.g., wss://myhost:8443)")
	clientCmd.Flags().StringP("name", "n", "", "tunnel name (used for identification)")
	clientCmd.Flags().StringSlice("expose", []string{"22"}, "ports to expose (e.g., 22,80,443)")
	clientCmd.Flags().StringSlice("expose-subnets", nil, "subnets reachable via this client")
	clientCmd.Flags().Bool("reconnect", true, "auto-reconnect on disconnect")
	clientCmd.Flags().Duration("reconnect-interval", 5*time.Second, "base reconnect interval")
	clientCmd.Flags().String("ssh-key", "", "path to SSH private key (default: use ssh-agent)")
	clientCmd.Flags().Bool("insecure", false, "skip TLS certificate verification")
	clientCmd.Flags().Bool("no-auth", false, "skip SSH authentication (for testing only)")
	clientCmd.Flags().Bool("tun", false, "enable TUN mode (requires root/CAP_NET_ADMIN)")

	viper.BindPFlag("client.name", clientCmd.Flags().Lookup("name"))
	// Note: server and name are validated at runtime (not MarkFlagRequired)
	// so they can be provided via config file instead of CLI flags.
	viper.BindPFlag("client.name", clientCmd.Flags().Lookup("name"))
	viper.BindPFlag("client.expose", clientCmd.Flags().Lookup("expose"))
	viper.BindPFlag("client.expose_subnets", clientCmd.Flags().Lookup("expose-subnets"))
	viper.BindPFlag("client.reconnect", clientCmd.Flags().Lookup("reconnect"))
	viper.BindPFlag("client.reconnect_interval", clientCmd.Flags().Lookup("reconnect-interval"))
	viper.BindPFlag("client.ssh_key", clientCmd.Flags().Lookup("ssh-key"))
	viper.BindPFlag("client.insecure", clientCmd.Flags().Lookup("insecure"))
	viper.BindPFlag("client.no_auth", clientCmd.Flags().Lookup("no-auth"))
	viper.BindPFlag("client.tun", clientCmd.Flags().Lookup("tun"))
}

func runClient(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadClient()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.Server == "" {
		return fmt.Errorf("server address is required (--server or config file)")
	}
	if cfg.Name == "" {
		return fmt.Errorf("tunnel name is required (--name or config file)")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel(),
	}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("shutting down client")
		cancel()
	}()

	logger.Info("connecting to server", "server", cfg.Server, "name", cfg.Name)

	var conn *transport.Client
	if cfg.NoAuth {
		logger.Warn("authentication disabled — do not use in production")
		conn = transport.NewClientNoAuth(cfg, logger)
	} else {
		conn = transport.NewClient(cfg, logger)
	}

	// Enable TUN mode if requested
	if cfg.TUN {
		logger.Info("TUN mode enabled — will create device when server assigns IP")
		conn.EnableTUN(func(assignedIP string) (io.ReadWriteCloser, error) {
			dev, err := tun.New(tun.Config{
				Name: "rtun0",
				IP:   assignedIP,
				MTU:  1420,
			})
			if err != nil {
				return nil, err
			}
			logger.Info("TUN device ready", "name", dev.Name(), "ip", assignedIP)
			return dev, nil
		})
	}

	return conn.Run(ctx)
}
