package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/malevolent/rtunnel/pkg/tunnel"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show connected clients on the server",
	Long: `Queries the rtunnel server's status API and displays
currently connected clients, their assigned IPs, and exposed ports.`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().StringP("server", "s", "", "server address (e.g., http://192.168.1.100:8444)")
	statusCmd.MarkFlagRequired("server")
}

func runStatus(cmd *cobra.Command, args []string) error {
	server, _ := cmd.Flags().GetString("server")

	// Normalize URL
	server = strings.TrimSuffix(server, "/")
	if !strings.HasPrefix(server, "http") {
		server = "http://" + server
	}
	// Convert ws:// to http://
	server = strings.Replace(server, "ws://", "http://", 1)
	server = strings.Replace(server, "wss://", "https://", 1)

	url := server + "/api/status"

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("connecting to server: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	var status struct {
		Tunnels []tunnel.TunnelInfo `json:"tunnels"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	if len(status.Tunnels) == 0 {
		fmt.Println("No clients connected.")
		return nil
	}

	fmt.Printf("%-4s  %-20s  %-18s  %-24s  %s\n", "ID", "NAME", "TUNNEL IP", "REMOTE ADDR", "EXPOSE PORTS")
	fmt.Printf("%-4s  %-20s  %-18s  %-24s  %s\n", "──", "────", "─────────", "───────────", "────────────")
	for _, t := range status.Tunnels {
		ports := "—"
		if len(t.ExposePorts) > 0 {
			parts := make([]string, len(t.ExposePorts))
			for i, p := range t.ExposePorts {
				parts[i] = fmt.Sprintf("%d", p)
			}
			ports = strings.Join(parts, ", ")
		}
		fmt.Printf("%-4d  %-20s  %-18s  %-24s  %s\n", t.ID, t.Name, t.AssignedIP, t.RemoteAddr, ports)
	}

	return nil
}
