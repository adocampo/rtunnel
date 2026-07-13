package tunnel

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/malevolent/rtunnel/pkg/config"
)

func TestHandleStatus_NoTunnels(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ServerConfig{IPPool: "10.99.0.0/16", Mode: "tun"}
	mgr := NewManager(cfg, logger)

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	mgr.HandleStatus()(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Tunnels []TunnelInfo `json:"tunnels"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Tunnels) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(body.Tunnels))
	}
}

func TestHandleStatus_WithTunnel(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ServerConfig{IPPool: "10.99.0.0/16", Mode: "tun"}
	mgr := NewManager(cfg, logger)

	// Manually inject a tunnel for testing
	mgr.mu.Lock()
	ip := mgr.ipPool.GatewayIP()
	ip2, _ := mgr.ipPool.Allocate()
	_ = ip
	mgr.tunnels["test-client"] = &Tunnel{
		ID:          1,
		Name:        "test-client",
		RemoteAddr:  "192.168.1.50:12345",
		AssignedIP:  ip2,
		ExposePorts: []int{22, 80},
		streams:     make(map[uint32]*Stream),
	}
	mgr.byIP[ip2.String()] = mgr.tunnels["test-client"]
	mgr.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	mgr.HandleStatus()(w, req)

	var body struct {
		Tunnels []TunnelInfo `json:"tunnels"`
	}
	json.NewDecoder(w.Result().Body).Decode(&body)
	if len(body.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(body.Tunnels))
	}
	tun := body.Tunnels[0]
	if tun.Name != "test-client" {
		t.Errorf("expected name test-client, got %s", tun.Name)
	}
	if tun.AssignedIP != ip2.String() {
		t.Errorf("expected IP %s, got %s", ip2.String(), tun.AssignedIP)
	}
	if len(tun.ExposePorts) != 2 || tun.ExposePorts[0] != 22 {
		t.Errorf("expected expose [22 80], got %v", tun.ExposePorts)
	}
}

func TestIPPool_AllocateAndRelease(t *testing.T) {
	pool, err := NewIPPool("10.99.0.0/24")
	if err != nil {
		t.Fatal(err)
	}

	gw := pool.GatewayIP()
	if gw.String() != "10.99.0.1" {
		t.Errorf("expected gateway 10.99.0.1, got %s", gw)
	}

	ip1, err := pool.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if ip1.String() != "10.99.0.2" {
		t.Errorf("expected first alloc 10.99.0.2, got %s", ip1)
	}

	ip2, err := pool.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if ip2.String() != "10.99.0.3" {
		t.Errorf("expected second alloc 10.99.0.3, got %s", ip2)
	}

	// Release ip1 and allocate again — pool advances, so next is .4
	pool.Release(ip1)
	ip3, err := pool.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if ip3.String() != "10.99.0.4" {
		t.Errorf("expected next alloc 10.99.0.4 (pool advances), got %s", ip3)
	}
}

func TestIPPool_Exhaustion(t *testing.T) {
	// /29 = 8 addrs: .0(net), .1(gw), .2-.6(hosts), .7(broadcast)
	// pool starts at .2, so can allocate .2-.7 (6 IPs, since Contains includes broadcast)
	// Actually the pool allocates until Contains returns false
	pool, err := NewIPPool("10.99.0.0/29")
	if err != nil {
		t.Fatal(err)
	}

	// Exhaust the pool: .2 through .7
	var allocated int
	for i := 0; i < 20; i++ {
		_, err := pool.Allocate()
		if err != nil {
			break
		}
		allocated++
	}
	if allocated == 0 {
		t.Error("expected at least 1 allocation")
	}
	// Try one more — should fail
	_, err = pool.Allocate()
	if err == nil {
		t.Error("expected pool exhaustion error after filling pool")
	}
}
