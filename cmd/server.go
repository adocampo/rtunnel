package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/malevolent/rtunnel/pkg/auth"
	"github.com/malevolent/rtunnel/pkg/config"
	"github.com/malevolent/rtunnel/pkg/socks"
	"github.com/malevolent/rtunnel/pkg/tun"
	"github.com/malevolent/rtunnel/pkg/tunnel"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the rtunnel server (receives connections from clients)",
	Long: `The server runs on your machine (the one with network access).
It listens for incoming WebSocket connections from rtunnel clients,
creates TUN interfaces or SOCKS proxies, and exposes the remote
machines to your local network.`,
	RunE: runServer,
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringP("listen", "l", ":8443", "address to listen on")
	serverCmd.Flags().String("tls-cert", "", "TLS certificate file")
	serverCmd.Flags().String("tls-key", "", "TLS private key file")
	serverCmd.Flags().String("authorized-keys", "~/.ssh/authorized_keys", "authorized SSH public keys file")
	serverCmd.Flags().String("ip-pool", "10.99.0.0/16", "IP pool for tunnel allocation")
	serverCmd.Flags().String("mode", "tun", "default tunnel mode: tun or socks")
	serverCmd.Flags().String("socks-listen", ":1080", "SOCKS5 proxy listen address (used when mode=socks)")
	serverCmd.Flags().String("socks-tunnel", "", "tunnel name to route SOCKS through (default: first connected)")
	serverCmd.Flags().Bool("no-auth", false, "disable SSH authentication (for testing only)")

	viper.BindPFlag("server.listen", serverCmd.Flags().Lookup("listen"))
	viper.BindPFlag("server.tls.cert", serverCmd.Flags().Lookup("tls-cert"))
	viper.BindPFlag("server.tls.key", serverCmd.Flags().Lookup("tls-key"))
	viper.BindPFlag("server.authorized_keys", serverCmd.Flags().Lookup("authorized-keys"))
	viper.BindPFlag("server.ip_pool", serverCmd.Flags().Lookup("ip-pool"))
	viper.BindPFlag("server.mode", serverCmd.Flags().Lookup("mode"))
	viper.BindPFlag("server.socks_listen", serverCmd.Flags().Lookup("socks-listen"))
	viper.BindPFlag("server.socks_tunnel", serverCmd.Flags().Lookup("socks-tunnel"))
	viper.BindPFlag("server.no_auth", serverCmd.Flags().Lookup("no-auth"))
}

func runServer(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadServer()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel(),
	}))
	slog.SetDefault(logger)

	manager := tunnel.NewManager(cfg, logger)

	mux := http.NewServeMux()
	if cfg.NoAuth {
		logger.Warn("authentication disabled — do not use in production")
		mux.HandleFunc("/tunnel", manager.HandleNoAuth())
	} else {
		verifier, err := auth.NewSSHVerifier(cfg.AuthorizedKeys)
		if err != nil {
			return fmt.Errorf("loading authorized keys: %w", err)
		}
		mux.HandleFunc("/tunnel", manager.HandleConnection(verifier))
	}

	server := &http.Server{
		Addr:    cfg.Listen,
		Handler: mux,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("shutting down server")
		cancel()
		server.Close()
	}()

	// Start SOCKS5 proxy if mode=socks
	if cfg.Mode == "socks" {
		socksAddr := cfg.SocksListen
		if socksAddr == "" {
			socksAddr = ":1080"
		}
		socksTunnel := cfg.SocksTunnel
		if socksTunnel == "" {
			socksTunnel = "*" // special: route to first available tunnel
		}
		socksServer, err := socks.NewServer(socksAddr, manager, socksTunnel, logger)
		if err != nil {
			return fmt.Errorf("starting SOCKS5 proxy: %w", err)
		}
		go socksServer.Serve(ctx)
		logger.Info("SOCKS5 proxy started", "addr", socksServer.Addr(), "tunnel", socksTunnel)
	}

	// Create TUN device if mode=tun
	if cfg.Mode == "tun" {
		_, gwNet, err := parseIPPool(cfg.IPPool)
		if err != nil {
			return fmt.Errorf("parsing IP pool for TUN: %w", err)
		}
		tunDev, tunErr := tun.New(tun.Config{
			Name: "rtun0",
			IP:   gwNet,
			MTU:  1420,
		})
		if tunErr != nil {
			return fmt.Errorf("creating TUN device: %w", tunErr)
		}
		defer tunDev.Close()
		manager.SetTUN(tunDev)
		go manager.ReadTUNLoop(ctx)
		logger.Info("TUN device created", "name", tunDev.Name(), "ip", gwNet)
	}

	logger.Info("starting rtunnel server", "addr", cfg.Listen, "mode", cfg.Mode)

	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		err = server.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
	} else {
		logger.Warn("running without TLS — use only for testing")
		err = server.ListenAndServe()
	}

	_ = ctx
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// parseIPPool returns the gateway IP with CIDR notation for the TUN device.
// e.g., "10.99.0.0/16" -> "10.99.0.1/16"
func parseIPPool(cidr string) (gateway string, gatewayCIDR string, err error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", err
	}
	// Gateway is .1 in the network (use ipNet.IP length, not ip length)
	gw := make(net.IP, len(ipNet.IP))
	copy(gw, ipNet.IP)
	gw[len(gw)-1] = 1
	mask, _ := ipNet.Mask.Size()
	return gw.String(), fmt.Sprintf("%s/%d", gw.String(), mask), nil
}
