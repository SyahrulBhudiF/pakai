package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dhanifudin/pakai/internal/schema"
)

// Client is an HTTP client for communicating with the daemon.
type Client struct {
	baseURL string
	port    int
	http    *http.Client
}

// New creates a new daemon client.
func New(port int) *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		port:    port,
		http: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Health checks if the daemon is healthy.
func (c *Client) Health() error {
	resp, err := c.http.Get(c.baseURL + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}
	return nil
}

// Status fetches the current usage status from the daemon.
func (c *Client) Status() ([]*schema.Usage, error) {
	resp, err := c.http.Get(c.baseURL + "/status")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}

	var usages []*schema.Usage
	if err := json.NewDecoder(resp.Body).Decode(&usages); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return usages, nil
}

// Events subscribes to the SSE event stream and calls the callback for each event.
func (c *Client) Events(ctx context.Context, callback func([]*schema.Usage)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/events", nil)
	if err != nil {
		return err
	}

	sseClient := &http.Client{Timeout: 0} // No timeout for SSE
	resp, err := sseClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to events: %w", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		var usages []*schema.Usage
		if err := json.Unmarshal([]byte(data), &usages); err != nil {
			continue
		}
		callback(usages)
	}

	return scanner.Err()
}

// EnsureRunning ensures the daemon is running and has completed a first refresh,
// spawning it if necessary. Returns nil once the daemon has usage data to report,
// or if it is running with no providers to poll. Returns an error if the daemon
// never becomes reachable within the timeout.
//
// /health answers as soon as the HTTP server is up, which is before the refresh
// loop has produced its first snapshot. Readiness is therefore a non-empty
// /status, not a live /health.
func EnsureRunning(port int) error {
	c := New(port)
	if err := c.Health(); err != nil {
		// Check via TCP dial first
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
		} else {
			// Try to spawn the daemon as detached process
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to get executable: %w", err)
			}

			cmd := exec.Command(exe, "daemon", "start")
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("failed to start daemon: %w", err)
			}
		}
	}

	// Poll every 200ms up to 3 seconds for the first snapshot.
	deadline := time.Now().Add(3 * time.Second)
	for {
		usages, statusErr := c.Status()
		if statusErr == nil && len(usages) > 0 {
			return nil
		}

		// A daemon whose provider list is empty never produces a snapshot, so
		// waiting for one would only stall the caller.
		health, healthErr := c.GetHealth()
		if healthErr == nil && len(health.Providers) == 0 {
			return nil
		}

		if !time.Now().Before(deadline) {
			if healthErr == nil {
				return nil
			}
			return fmt.Errorf("daemon failed to start within 3 seconds")
		}

		time.Sleep(200 * time.Millisecond)
	}
}

// PIDFilePath returns the path to the daemon PID file.
func PIDFilePath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		uid := fmt.Sprintf("%d", os.Getuid())
		dir = "/run/user/" + uid
	}
	return filepath.Join(dir, "pakai", "daemon.pid")
}

// HealthResponse represents the response from /health.
type HealthResponse struct {
	Status        string    `json:"status"`
	UptimeSeconds int       `json:"uptime_seconds"`
	Connections   int       `json:"connections"`
	Port          int       `json:"port"`
	Providers     []string  `json:"providers"`
	Version       string    `json:"version"`
	BinaryMtime   time.Time `json:"binary_mtime"`
}

// IsStale reports whether the running daemon was built from an older binary than
// the currently installed one. It checks both the version string (for tagged
// releases) and the executable mtime (for dev builds where version is always
// "dev"). A 2s grace period avoids false positives from filesystem timestamp
// imprecision.
func IsStale(current string, h *HealthResponse) bool {
	if current != h.Version {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	st, err := os.Stat(exe)
	if err != nil {
		return false
	}
	return st.ModTime().After(h.BinaryMtime.Add(2 * time.Second))
}

// GetHealth returns the health status of the daemon.
func (c *Client) GetHealth() (*HealthResponse, error) {
	resp, err := c.http.Get(c.baseURL + "/health")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var h HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, err
	}
	return &h, nil
}
