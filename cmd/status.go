package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/malevolent/rtunnel/pkg/tunnel"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show tunnel status, health checks, and connected clients",
	Long: `Performs local health checks (config, service, TUN interface) and
queries the rtunnel server's status API to display connected clients.`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().StringP("server", "s", "", "server address (e.g., http://192.168.1.100:8444)")
}

func runStatus(cmd *cobra.Command, args []string) error {
	fmt.Println("rtunnel status")
	fmt.Println(strings.Repeat("─", 60))

	// ─── Config ──────────────────────────────────────────────────
	configFile := viper.ConfigFileUsed()
	if configFile != "" {
		fmt.Printf("  Config:    %s ✓\n", configFile)
		mode := detectMode()
		if mode == "server" {
			listen := viper.GetString("server.listen")
			if listen == "" || listen == ":8443" {
				fmt.Printf("  WARNING:   server.listen is default (%s) — verify this is intentional\n", listen)
			} else {
				fmt.Printf("  Listen:    %s\n", listen)
			}
			fmt.Printf("  Mode:      %s\n", viper.GetString("server.mode"))
			fmt.Printf("  IP Pool:   %s\n", viper.GetString("server.ip_pool"))
		} else if mode == "client" {
			server := viper.GetString("client.server")
			if server == "" {
				fmt.Println("  ERROR:     client.server is empty in config!")
			} else {
				fmt.Printf("  Server:    %s\n", server)
			}
			name := viper.GetString("client.name")
			if name == "" {
				fmt.Println("  ERROR:     client.name is empty in config!")
			} else {
				fmt.Printf("  Name:      %s\n", name)
			}
			expose := viper.GetIntSlice("client.expose")
			if len(expose) > 0 {
				fmt.Printf("  Expose:    %v\n", expose)
			}
			fmt.Printf("  TUN:       %v\n", viper.GetBool("client.tun"))
		} else {
			fmt.Println("  WARNING:   config has neither server nor client section")
		}
	} else {
		fmt.Println("  Config:    not found (searched ./, ~/.config/rtunnel/, /etc/rtunnel/)")
	}
	fmt.Println()

	// ─── Service ─────────────────────────────────────────────────
	fmt.Println("  Service:")
	switch runtime.GOOS {
	case "linux":
		checkSystemdService("rtunnel-server")
		checkSystemdService("rtunnel-client")
	case "darwin":
		checkLaunchdService()
	}
	fmt.Println()

	// ─── TUN Interface ───────────────────────────────────────────
	fmt.Println("  TUN Interface:")
	tunFound := false
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Name, "utun") || strings.HasPrefix(iface.Name, "rtun") || strings.HasPrefix(iface.Name, "tun") {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if strings.Contains(addr.String(), "10.99.") {
					fmt.Printf("    %s: %s ✓\n", iface.Name, addr.String())
					tunFound = true
				}
			}
		}
	}
	if !tunFound {
		fmt.Println("    No active tunnel interface found")
	}
	fmt.Println()

	// ─── Server Connectivity & Tunnels ───────────────────────────
	server, _ := cmd.Flags().GetString("server")
	if server == "" {
		server = viper.GetString("client.server")
	}
	if server == "" {
		server = viper.GetString("server.listen")
	}

	if server == "" {
		fmt.Println("  Server:    unknown (no server address in config or flags)")
		return nil
	}

	// Normalize URL
	server = strings.TrimSuffix(server, "/")
	server = strings.Replace(server, "wss://", "https://", 1)
	server = strings.Replace(server, "ws://", "http://", 1)
	if !strings.HasPrefix(server, "http") {
		server = "http://" + server
	}

	url := server + "/api/status"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(url)
	if err != nil {
		fmt.Printf("  Server:    %s ✗ unreachable\n", server)
		fmt.Printf("             %v\n", err)
		return nil
	}
	defer resp.Body.Close()

	fmt.Printf("  Server:    %s ✓\n", server)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("  API:       error reading response: %v\n", err)
		return nil
	}

	var status struct {
		Tunnels []tunnel.TunnelInfo `json:"tunnels"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		fmt.Printf("  API:       error parsing response: %v\n", err)
		return nil
	}

	fmt.Println()
	if len(status.Tunnels) == 0 {
		fmt.Println("  Connected Clients: none")
	} else {
		fmt.Printf("  Connected Clients: %d\n", len(status.Tunnels))
		fmt.Printf("  %-4s  %-20s  %-18s  %-24s  %s\n", "ID", "NAME", "TUNNEL IP", "REMOTE ADDR", "EXPOSE PORTS")
		fmt.Printf("  %-4s  %-20s  %-18s  %-24s  %s\n", "──", "────", "─────────", "───────────", "────────────")
		for _, t := range status.Tunnels {
			ports := "—"
			if len(t.ExposePorts) > 0 {
				parts := make([]string, len(t.ExposePorts))
				for i, p := range t.ExposePorts {
					parts[i] = fmt.Sprintf("%d", p)
				}
				ports = strings.Join(parts, ", ")
			}
			fmt.Printf("  %-4d  %-20s  %-18s  %-24s  %s\n", t.ID, t.Name, t.AssignedIP, t.RemoteAddr, ports)
		}
	}

	return nil
}

func detectMode() string {
	if viper.IsSet("client.server") || viper.IsSet("client.name") {
		return "client"
	}
	if viper.IsSet("server.listen") || viper.IsSet("server.mode") {
		return "server"
	}
	return ""
}

func checkSystemdService(name string) {
	out, err := exec.Command("systemctl", "is-active", name).Output()
	if err != nil {
		return
	}
	state := strings.TrimSpace(string(out))
	if state == "active" {
		fmt.Printf("    %s: active ✓\n", name)
	} else {
		fmt.Printf("    %s: %s ✗\n", name, state)
		out, _ := exec.Command("journalctl", "-u", name, "--no-pager", "-n", "3", "-q").Output()
		if len(out) > 0 {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				fmt.Printf("      %s\n", line)
			}
		}
	}
}

func checkLaunchdService() {
	plist := "/Library/LaunchDaemons/com.rtunnel.client.plist"
	if _, err := os.Stat(plist); err != nil {
		fmt.Println("    com.rtunnel.client: not installed")
		return
	}
	out, err := exec.Command("launchctl", "print", "system/com.rtunnel.client").CombinedOutput()
	if err != nil {
		fmt.Println("    com.rtunnel.client: not loaded ✗")
		return
	}
	if strings.Contains(string(out), "state = running") {
		fmt.Println("    com.rtunnel.client: running ✓")
	} else {
		fmt.Println("    com.rtunnel.client: not running ✗")
	}
}
