package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/malevolent/rtunnel/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Run a relay server (bridges clients and servers without direct connectivity)",
	Long: `The relay acts as a middleman when the server doesn't have a public IP.
Both the server and client connect to the relay, which matches them by
tunnel name and forwards WebSocket frames transparently.`,
	RunE: runRelay,
}

func init() {
	rootCmd.AddCommand(relayCmd)
	relayCmd.Flags().StringP("listen", "l", ":8443", "address to listen on")
	relayCmd.Flags().String("tls-cert", "", "TLS certificate file")
	relayCmd.Flags().String("tls-key", "", "TLS private key file")

	viper.BindPFlag("relay.listen", relayCmd.Flags().Lookup("listen"))
	viper.BindPFlag("relay.tls.cert", relayCmd.Flags().Lookup("tls-cert"))
	viper.BindPFlag("relay.tls.key", relayCmd.Flags().Lookup("tls-key"))
}

func runRelay(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadRelay()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel(),
	}))
	slog.SetDefault(logger)

	// TODO: implement relay logic
	mux := http.NewServeMux()
	mux.HandleFunc("/relay", func(w http.ResponseWriter, r *http.Request) {
		// Placeholder — will implement relay matching logic
		http.Error(w, "relay not yet implemented", http.StatusNotImplemented)
	})

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
		logger.Info("shutting down relay")
		cancel()
		server.Close()
	}()

	logger.Info("starting rtunnel relay", "addr", cfg.Listen)

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
