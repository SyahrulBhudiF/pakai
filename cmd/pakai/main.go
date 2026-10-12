package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dhanifudin/pakai/internal/client"
	"github.com/dhanifudin/pakai/internal/config"
	"github.com/dhanifudin/pakai/internal/daemon"
	"github.com/dhanifudin/pakai/internal/detect"
	"github.com/dhanifudin/pakai/internal/providers"
	claudeprov "github.com/dhanifudin/pakai/internal/providers/claude"
	"github.com/dhanifudin/pakai/internal/providers/codex"
	inferhubprovider "github.com/dhanifudin/pakai/internal/providers/inferhub"
	"github.com/dhanifudin/pakai/internal/providers/mock"
	opencodeprov "github.com/dhanifudin/pakai/internal/providers/opencode"
	opencodegoprov "github.com/dhanifudin/pakai/internal/providers/opencodego"
	piprovider "github.com/dhanifudin/pakai/internal/providers/pi"
	"github.com/dhanifudin/pakai/internal/renderer"
	"github.com/dhanifudin/pakai/internal/schema"
	"github.com/dhanifudin/pakai/internal/systemd"
	"github.com/dhanifudin/pakai/internal/widgets"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "pakai",
	Short: "PakAI — unified AI subscription usage tracker",
	Long:  "PakAI tracks AI usage from local sources and surfaces it via tmux, waybar, CLI status, and a TUI dashboard.",
}

func main() {
	rootCmd.SetErr(os.Stderr)

	// Initialize config
	if err := config.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load config: %v\n", err)
	}

	rootCmd.AddCommand(
		newVersionCmd(),
		newStatusCmd(),
		newTmuxCmd(),
		newWaybarCmd(),
		newDaemonCmd(),
		newSetupCmd(),
		newConfigCmd(),
		newProviderCmd(),
		newDashboardCmd(),
		newCompletionCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// fetchUsages retrieves usage data, trying daemon first, falling back to direct fetch.
func fetchUsages() ([]*schema.Usage, time.Time, error) {
	port := config.GetDaemonPort()
	c := client.New(port)

	// Try daemon first
	if err := c.Health(); err == nil {
		usages, err := c.Status()
		if err == nil {
			// Find most recent refreshedAt
			var refreshedAt time.Time
			for _, u := range usages {
				if u.RefreshedAt.After(refreshedAt) {
					refreshedAt = u.RefreshedAt
				}
			}
			return usages, refreshedAt, nil
		}
	}

	// Try to auto-spawn daemon
	if err := client.EnsureRunning(port); err == nil {
		usages, err := c.Status()
		if err == nil {
			var refreshedAt time.Time
			for _, u := range usages {
				if u.RefreshedAt.After(refreshedAt) {
					refreshedAt = u.RefreshedAt
				}
			}
			return usages, refreshedAt, nil
		}
	}

	// Fall back to direct fetch (degraded mode)
	return fetchDirect()
}

// fetchDirect fetches usage data directly from providers (degraded mode).
func fetchDirect() ([]*schema.Usage, time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var provs []providers.Provider

	// Check mocked providers
	mockedIDs, _ := mock.ListMocked()
	mockedSet := make(map[string]bool)
	for _, id := range mockedIDs {
		mp, err := mock.Load(id)
		if err == nil {
			provs = append(provs, mp)
			mockedSet[id] = true
		}
	}

	if config.IsProviderEnabled("claude") && !mockedSet["claude"] {
		provs = append(provs, claudeprov.New())
	}
	if config.IsProviderEnabled("opencode") && !mockedSet["opencode"] {
		provs = append(provs, opencodeprov.New())
	}
	if config.IsProviderEnabled("openai") && !mockedSet["openai"] {
		provs = append(provs, codex.New())
	}
	if config.IsProviderEnabled("opencode-go") && !mockedSet["opencode-go"] {
		provs = append(provs, opencodegoprov.New())
	}
	if config.IsProviderEnabled("pi") && !mockedSet["pi"] {
		provs = append(provs, piprovider.New())
	}
	if inferhubprovider.Enabled() && !mockedSet["inferhub"] {
		provs = append(provs, inferhubprovider.New())
	}

	var usages []*schema.Usage
	now := time.Now()

	for _, p := range provs {
		type multiProvider interface {
			FetchAll(ctx context.Context) ([]*schema.Usage, error)
		}

		if mp, ok := p.(multiProvider); ok {
			results, err := mp.FetchAll(ctx)
			if err != nil {
				usages = append(usages, &schema.Usage{
					Provider:    p.ID(),
					Label:       p.ID(),
					Status:      schema.StatusError,
					Error:       err.Error(),
					RefreshedAt: now,
				})
				continue
			}
			usages = append(usages, results...)
			continue
		}

		u, err := p.Fetch(ctx)
		if err != nil {
			usages = append(usages, &schema.Usage{
				Provider:    p.ID(),
				Label:       p.ID(),
				Status:      schema.StatusError,
				Error:       err.Error(),
				RefreshedAt: now,
			})
			continue
		}
		usages = append(usages, u)
	}

	return usages, now, nil
}

// -- version command --

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the PakAI version",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println(version)
			return nil
		},
	}
}

// -- status command --

func newStatusCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show usage status for all providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput {
				return statusJSON()
			}
			port := config.GetDaemonPort()
			if h, err := client.New(port).GetHealth(); err == nil && client.IsStale(version, h) {
				fmt.Fprintln(os.Stderr, "⚠ daemon is running an older build — run 'pakai daemon restart' to pick up the new binary")
			}
			usages, refreshedAt, err := fetchUsages()
			if err != nil {
				return err
			}
			fmt.Print(renderer.RenderStatus(usages, refreshedAt))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output raw JSON (same as GET /status)")
	return cmd
}

func statusJSON() error {
	port := config.GetDaemonPort()
	c := client.New(port)

	if err := client.EnsureRunning(port); err != nil {
		return fmt.Errorf("daemon not available: %w", err)
	}

	usages, err := c.Status()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(usages, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// -- tmux command --

func newTmuxCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tmux",
		Short: "Output compact string for tmux status bar",
		RunE: func(cmd *cobra.Command, args []string) error {
			usages, _, err := fetchUsages()
			if err != nil {
				return err
			}
			fmt.Println(renderer.RenderTmux(usages, config.GetSeparator()))
			return nil
		},
	}
}

// -- waybar command --

func newWaybarCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "waybar",
		Short: "Output JSON for waybar module",
		RunE: func(cmd *cobra.Command, args []string) error {
			usages, _, err := fetchUsages()
			if err != nil {
				return err
			}
			fmt.Println(renderer.RenderWaybar(usages, config.GetSeparator()))
			return nil
		},
	}
}

// -- dashboard command --

func newDashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Launch interactive TUI dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			port := config.GetDaemonPort()
			_ = client.EnsureRunning(port)
			return renderer.RunDashboard(port)
		},
	}
}

// -- daemon command --

func newDaemonCmd() *cobra.Command {
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the background daemon",
	}

	daemonCmd.AddCommand(
		newDaemonStartCmd(),
		newDaemonStopCmd(),
		newDaemonRestartCmd(),
		newDaemonStatusCmd(),
	)

	return daemonCmd
}

func newDaemonStartCmd() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the background daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			port := config.GetDaemonPort()

			if daemon.IsRunning(port) {
				fmt.Println("Daemon is already running.")
				return nil
			}

			if foreground {
				return runDaemonForeground(port)
			}

			// Fork as background process
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %w", err)
			}

			daemonCmd := make([]string, 0, len(os.Args))
			for _, a := range os.Args[1:] {
				if a != "start" {
					daemonCmd = append(daemonCmd, a)
				}
			}

			procAttr := &os.ProcAttr{
				Files: []*os.File{nil, nil, nil},
				Sys: &syscall.SysProcAttr{
					Setsid: true,
				},
			}

			proc, err := os.StartProcess(exe, append([]string{exe, "daemon", "start", "--foreground"}, daemonCmd...), procAttr)
			if err != nil {
				return fmt.Errorf("failed to start daemon: %w", err)
			}
			proc.Release()

			// Wait for daemon to be ready
			c := client.New(port)
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				if err := c.Health(); err == nil {
					fmt.Printf("Daemon started (port %d)\n", port)
					return nil
				}
			}

			fmt.Println("Daemon started (may still be initializing)")
			return nil
		},
	}

	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run in foreground (used internally)")
	return cmd
}

func runDaemonForeground(port int) error {
	srv, err := daemon.NewServer(port, version)
	if err != nil {
		return fmt.Errorf("failed to create daemon server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	return srv.Start()
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := daemon.GetPID()
			if err != nil {
				fmt.Println("Daemon is not running.")
				return nil
			}

			proc, err := os.FindProcess(pid)
			if err != nil {
				fmt.Printf("Failed to find daemon process (PID %d): %v\n", pid, err)
				return nil
			}

			if err := proc.Signal(syscall.SIGTERM); err != nil {
				fmt.Printf("Failed to stop daemon (PID %d): %v\n", pid, err)
				return err
			}

			// Wait up to 5 seconds for the process to exit
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err := proc.Signal(syscall.Signal(0)); err != nil {
					fmt.Printf("Daemon stopped (PID %d)\n", pid)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}

			fmt.Printf("Daemon stop signal sent (PID %d) — process may still be draining\n", pid)
			return nil
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			port := config.GetDaemonPort()
			c := client.New(port)

			h, err := c.GetHealth()
			if err != nil {
				pid, pidErr := daemon.GetPID()
				if pidErr != nil {
					fmt.Println("Daemon: not running")
				} else {
					fmt.Printf("Daemon: PID file found (%d) but not responding\n", pid)
				}
				return nil
			}

			pid, _ := daemon.GetPID()
			uptimeDur := time.Duration(h.UptimeSeconds) * time.Second
			fmt.Printf("Daemon: running (PID %d, port %d, uptime %s)\n", pid, port, uptimeDur)
			if client.IsStale(version, h) {
				fmt.Fprintln(os.Stderr, "⚠ daemon is running an older build — run 'pakai daemon restart' to pick up the new binary")
			}
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the background daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			port := config.GetDaemonPort()

			pid, err := daemon.GetPID()
			if err == nil {
				proc, err := os.FindProcess(pid)
				if err == nil {
					_ = proc.Signal(syscall.SIGTERM)
					deadline := time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) {
						if err := proc.Signal(syscall.Signal(0)); err != nil {
							break
						}
						time.Sleep(100 * time.Millisecond)
					}
				}
			}

			if err := client.EnsureRunning(port); err != nil {
				return fmt.Errorf("failed to start daemon: %w", err)
			}
			fmt.Println("Daemon restarted.")
			return nil
		},
	}
}

// -- setup command --

func newSetupCmd() *cobra.Command {
	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "First-run setup and configuration helpers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup()
		},
	}

	setupCmd.AddCommand(
		newSetupWaybarCmd(),
		newSetupTmuxCmd(),
		newSetupPlasmaCmd(),
		newSetupDmsCmd(),
	)

	return setupCmd
}

func runSetup() error {
	detections := detect.Detect(nil)

	found := false
	for _, d := range detections {
		if d.Found {
			found = true
			fmt.Printf("  ✓ Detected: %s at %s\n", d.ProviderID, d.Path)
		} else {
			fmt.Printf("  ✗ Not found: %s (%s)\n", d.ProviderID, d.Path)
		}
	}

	if !found {
		fmt.Println("\nNo providers detected. Checked paths:")
		for _, d := range detections {
			fmt.Printf("  - %s\n", d.Path)
		}
		return fmt.Errorf("no providers found")
	}

	// Start daemon if not running
	port := config.GetDaemonPort()
	fmt.Println("")
	if daemon.IsRunning(port) {
		fmt.Println("Daemon: already running")
	} else {
		if err := client.EnsureRunning(port); err == nil {
			fmt.Println("Daemon: started")
		} else {
			fmt.Println("Daemon: failed to start (run: pakai daemon start)")
		}
	}

	// Write systemd user unit
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable: %w", err)
	}
	if err := systemd.WriteUnit(exe, nil); err == nil {
		fmt.Printf("Systemd unit: written to %s\n", systemd.UnitPath(nil))
		fmt.Println("  Enable with: systemctl --user enable --now pakai")
	} else {
		fmt.Printf("Warning: failed to write systemd unit: %v\n", err)
	}

	fmt.Println("")
	fmt.Println("tmux — add to your ~/.tmux.conf:")
	fmt.Printf("  set -g status-right \"#(%s tmux)\"\n", exe)
	fmt.Println("  set -g status-interval 30")
	fmt.Println("")
	fmt.Println("waybar — add to your config.jsonc:")
	fmt.Println(`  "custom/pakai": {`)
	fmt.Println(`    "exec": "pakai waybar",`)
	fmt.Println(`    "return-type": "json",`)
	fmt.Println(`    "interval": 30`)
	fmt.Println(`  }`)

	// Live preview (AC 5, 6)
	fmt.Println("")
	fmt.Println("--- Live Preview ---")
	if err := runLivePreview(); err != nil {
		fmt.Fprintf(os.Stderr, "Preview unavailable: %v\n", err)
	}

	return nil
}

func runLivePreview() error {
	port := config.GetDaemonPort()
	c := client.New(port)

	if err := client.EnsureRunning(port); err != nil {
		return fmt.Errorf("daemon not running and could not be started: %w", err)
	}

	usages, err := c.Status()
	if err != nil {
		return fmt.Errorf("failed to fetch status: %w", err)
	}

	separator := config.GetSeparator()
	fmt.Println("tmux:   " + renderer.RenderTmux(usages, separator))
	fmt.Println("waybar: " + renderer.RenderWaybar(usages, separator))
	return nil
}

func newSetupWaybarCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "waybar",
		Short: "Print waybar module config and CSS",
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				exe = "pakai"
			}

			fmt.Println("// waybar config (add to your config.jsonc or config.json):")
			fmt.Printf(`"custom/pakai": {
    "exec": "%s waybar",
    "return-type": "json",
    "interval": 60,
    "format": "{}",
    "tooltip": true
}
`, exe)

			fmt.Println("/* waybar CSS (add to your style.css): */")
			fmt.Println(`#custom-pakai {
    padding: 0 8px;
}
#custom-pakai.ok {
    color: @green;
}
#custom-pakai.warning {
    color: @yellow;
}
#custom-pakai.critical {
    color: @red;
}
#custom-pakai.over-limit {
    color: @red;
    font-weight: bold;
}`)
			return nil
		},
	}
}

func newSetupTmuxCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tmux",
		Short: "Print tmux config line",
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				exe = "pakai"
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "# Add to your ~/.tmux.conf:")
			fmt.Fprintf(out, "set -g status-right '#(%s tmux)'\n", exe)
			fmt.Fprintln(out, "set -g status-interval 30")
			return nil
		},
	}
}

func xdgDataHome() string {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".local", "share")
	}
	return filepath.Join(home, ".local", "share")
}

func xdgConfigHome() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".config")
	}
	return filepath.Join(home, ".config")
}

func newSetupPlasmaCmd() *cobra.Command {
	var install bool
	cmd := &cobra.Command{
		Use:   "plasma",
		Short: "Print KDE Plasma widget install steps (--install to perform them)",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dst := filepath.Join(xdgDataHome(), "plasma", "plasmoids", "com.dhanifudin.pakai")

			if !install {
				fmt.Fprintln(out, "# Install the KDE Plasma widget:")
				fmt.Fprintf(out, "cp -r internal/widgets/plasma %s\n", dst)
				fmt.Fprintln(out, "plasmashell --replace &    # or log out and back in")
				fmt.Fprintln(out, "")
				fmt.Fprintln(out, "# Then: right-click panel → Add or Remove Widgets → search PakAI → drag to panel")
				fmt.Fprintln(out, "")
				fmt.Fprintln(out, "# Or let pakai do it:")
				fmt.Fprintln(out, "pakai setup plasma --install")
				return nil
			}

			fmt.Fprintf(out, "Installing Plasma widget to %s ...\n", dst)
			if err := widgets.ExtractTo("plasma", dst); err != nil {
				return fmt.Errorf("extract plasma widget: %w", err)
			}
			fmt.Fprintln(out, "  ✓ Files written")

			if _, err := exec.LookPath("plasmashell"); err != nil {
				fmt.Fprintln(out, "  plasmashell not found — log out and back in to load the widget")
			} else {
				fmt.Fprintln(out, "  Reloading plasmashell ...")
				c := exec.Command("plasmashell", "--replace")
				c.Stdout = nil
				c.Stderr = nil
				if err := c.Start(); err != nil {
					fmt.Fprintln(out, "  Reload failed — log out and back in to load the widget")
				} else {
					fmt.Fprintln(out, "  ✓ plasmashell restarting")
				}
			}
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "Add widget: right-click panel → Add or Remove Widgets → search PakAI → drag to panel")
			return nil
		},
	}
	cmd.Flags().BoolVar(&install, "install", false, "perform the install instead of printing steps")
	return cmd
}

func newSetupDmsCmd() *cobra.Command {
	var install bool
	cmd := &cobra.Command{
		Use:   "dms",
		Short: "Print DankMaterialShell plugin install steps (--install to perform them)",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dst := filepath.Join(xdgConfigHome(), "DankMaterialShell", "plugins", "pakai")

			if !install {
				fmt.Fprintln(out, "# Install the DankMaterialShell plugin:")
				fmt.Fprintf(out, "mkdir -p %s\n", filepath.Dir(dst))
				fmt.Fprintf(out, "cp -r internal/widgets/dms %s\n", dst)
				fmt.Fprintln(out, "dms ipc call plugin-scan scan")
				fmt.Fprintln(out, "")
				fmt.Fprintln(out, "# Then: enable PakAI in DMS Settings → Plugins, add it to the DankBar layout")
				fmt.Fprintln(out, "")
				fmt.Fprintln(out, "# Or let pakai do it:")
				fmt.Fprintln(out, "pakai setup dms --install")
				return nil
			}

			fmt.Fprintf(out, "Installing DMS plugin to %s ...\n", dst)
			if err := widgets.ExtractTo("dms", dst); err != nil {
				return fmt.Errorf("extract dms plugin: %w", err)
			}
			fmt.Fprintln(out, "  ✓ Files written")

			if _, err := exec.LookPath("dms"); err != nil {
				fmt.Fprintln(out, "  dms not found — run: dms ipc call plugin-scan scan")
			} else {
				fmt.Fprintln(out, "  Scanning plugins ...")
				out2, err := exec.Command("dms", "ipc", "call", "plugin-scan", "scan").CombinedOutput()
				if err != nil {
					fmt.Fprintf(out, "  Scan failed (%v) — run: dms ipc call plugin-scan scan\n", err)
				} else {
					_ = out2
					fmt.Fprintln(out, "  ✓ Plugin scan done")
				}
			}
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "Enable: DMS Settings → Plugins → PakAI, then add to DankBar layout")
			return nil
		},
	}
	cmd.Flags().BoolVar(&install, "install", false, "perform the install instead of printing steps")
	return cmd
}

// -- config command --

func newConfigCmd() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Get and set configuration values",
	}
	configCmd.AddCommand(
		newConfigSetCmd(),
		newConfigGetCmd(),
		newConfigListCmd(),
	)
	return configCmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value := args[1]

			if err := config.SetKey(key, value); err != nil {
				return err
			}
			fmt.Printf("Set %s = %s\n", key, value)
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			val, err := config.GetKey(key)
			if err != nil {
				return err
			}
			fmt.Printf("%s = %v\n", key, val)
			return nil
		},
	}
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configuration values",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Config()
			fmt.Printf("daemon.port = %d\n", cfg.Daemon.Port)
			fmt.Printf("daemon.poll_interval = %d\n", cfg.Daemon.PollInterval)
			fmt.Printf("display.separator = %q\n", cfg.Display.Separator)
			fmt.Printf("display.order = %v\n", cfg.Display.Order)
			fmt.Printf("thresholds.warning = %d\n", cfg.Thresholds.Warning)
			fmt.Printf("thresholds.critical = %d\n", cfg.Thresholds.Critical)
			for id, p := range cfg.Provider {
				fmt.Printf("provider.%s.enabled = %v\n", id, p.Enabled)
				if p.Label != "" {
					fmt.Printf("provider.%s.label = %s\n", id, p.Label)
				}
				if p.Limit != nil {
					fmt.Printf("provider.%s.limit = %.2f\n", id, *p.Limit)
				}
				if p.Key != "" {
					fmt.Printf("provider.%s.key = %s\n", id, config.MaskKey(p.Key))
				}
			}
			return nil
		},
	}
}

// -- provider command --

func newProviderCmd() *cobra.Command {
	providerCmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage providers",
	}

	providerCmd.AddCommand(
		newProviderMockCmd(),
		newProviderUnmockCmd(),
		newProviderDebugCmd(),
	)

	return providerCmd
}

func newProviderMockCmd() *cobra.Command {
	var usedVal float64
	var limitVal float64
	var unitVal string

	cmd := &cobra.Command{
		Use:   "mock <id>",
		Short: "Create a mock provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			if unitVal == "" {
				unitVal = "messages"
			}

			mp := mock.New(id, usedVal, limitVal, unitVal)
			if err := mp.Save(); err != nil {
				return fmt.Errorf("failed to save mock: %w", err)
			}

			fmt.Printf("Mock provider %q created: used=%.2f limit=%.2f unit=%s\n",
				id, usedVal, limitVal, unitVal)
			return nil
		},
	}

	cmd.Flags().Float64Var(&usedVal, "used", 0, "Used amount")
	cmd.Flags().Float64Var(&limitVal, "limit", 0, "Limit amount (0 = no limit)")
	cmd.Flags().StringVar(&unitVal, "unit", "messages", "Unit (messages, tokens, usd)")

	return cmd
}

func newProviderUnmockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unmock <id>",
		Short: "Remove a mock provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := mock.Remove(id); err != nil {
				return fmt.Errorf("failed to remove mock: %w", err)
			}
			fmt.Printf("Mock provider %q removed\n", id)
			return nil
		},
	}
}

func newProviderDebugCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "debug <id>",
		Short: "Show debug information for a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			fmt.Printf("Provider: %s\n", id)

			switch strings.ToLower(id) {
			case "claude":
				p := claudeprov.New()
				path := p.CachePath()
				fmt.Printf("Scanning: %s\n", path)
				if _, err := os.Stat(path); err == nil {
					fmt.Printf("  ✓ Found: %s\n", path)
				} else {
					fmt.Printf("  ✗ Not found: %s\n", path)
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				u, err := p.Fetch(ctx)
				if err != nil {
					fmt.Printf("Fetch error: %v\n", err)
				} else {
					fmt.Printf("Status: %s\n", u.Status)
					fmt.Printf("Used: %s\n", u.FormatUsed())
					for _, w := range u.WindowsOrDefault() {
						if pct := w.Pct(); pct >= 0 {
							fmt.Printf("  - %s: %.0f%%\n", w.Label, pct)
						} else {
							fmt.Printf("  - %s: %s\n", w.Label, w.FormatUsed())
						}
					}
					if u.Error != "" {
						fmt.Printf("Error: %s\n", u.Error)
					}
				}

			case "opencode":
				p := opencodeprov.New()
				path := p.DBPath()
				fmt.Printf("Scanning: %s\n", path)
				if _, err := os.Stat(path); err == nil {
					fmt.Printf("  ✓ Found: %s\n", path)
				} else {
					fmt.Printf("  ✗ Not found: %s\n", path)
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				usages, err := p.FetchAll(ctx)
				if err != nil {
					fmt.Printf("Fetch error: %v\n", err)
				} else {
					fmt.Printf("Providers found: %d\n", len(usages))
					for _, u := range usages {
						fmt.Printf("  - %s: %s (status: %s)\n", u.Provider, u.FormatUsed(), u.Status)
					}
				}

			case "openai":
				p := codex.New()
				fmt.Printf("Scanning: %s\n", p.CredentialsPath())
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				u, err := p.Fetch(ctx)
				if err != nil {
					fmt.Printf("Fetch error: %v\n", err)
				} else {
					fmt.Printf("Status: %s\n", u.Status)
					for _, w := range u.WindowsOrDefault() {
						if pct := w.Pct(); pct >= 0 {
							fmt.Printf("  - %s: %.0f%%\n", w.Label, pct)
						} else {
							fmt.Printf("  - %s: %s\n", w.Label, w.FormatUsed())
						}
					}
					if u.Error != "" {
						fmt.Printf("Error: %s\n", u.Error)
					}
				}

			case "opencode-go":
				p := opencodegoprov.New()
				fmt.Printf("Scanning: %s\n", p.CredentialsPath())
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				u, err := p.Fetch(ctx)
				if err != nil {
					fmt.Printf("Fetch error: %v\n", err)
				} else {
					fmt.Printf("Status: %s\n", u.Status)
					for _, w := range u.WindowsOrDefault() {
						if pct := w.Pct(); pct >= 0 {
							fmt.Printf("  - %s: %.1f%%\n", w.Label, pct)
						} else {
							fmt.Printf("  - %s: %s\n", w.Label, w.FormatUsed())
						}
					}
					if u.Error != "" {
						fmt.Printf("Error: %s\n", u.Error)
					}
				}

			case "inferhub":
				p := inferhubprovider.New()
				fmt.Printf("Usage URL: %s\n", p.UsageURL())
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				u, err := p.Fetch(ctx)
				if err != nil {
					fmt.Printf("Fetch error: %v\n", err)
				} else {
					fmt.Printf("Status: %s\n", u.Status)
					for _, w := range u.Windows {
						if pct := w.Pct(); pct >= 0 {
							fmt.Printf("  - %s: %.1f%%\n", w.Label, pct)
						} else {
							fmt.Printf("  - %s: %s\n", w.Label, w.FormatUsed())
						}
					}
					if u.Error != "" {
						fmt.Printf("Error: %s\n", u.Error)
					}
				}

			default:
				// Check if it's a mock
				mp, err := mock.Load(id)
				if err != nil {
					fmt.Printf("Unknown provider: %s\n", id)
					fmt.Println("Known providers: claude, opencode, opencode-go, openai, pi, inferhub")
					return nil
				}

				path, _ := mock.MockFilePath(id)
				fmt.Printf("  Mock file: %s (✓)\n", path)

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				u, _ := mp.Fetch(ctx)
				fmt.Printf("Status: %s\n", u.Status)
				fmt.Printf("Used: %s\n", u.FormatUsed())
			}

			return nil
		},
	}
}

// -- completion command --

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish]",
		Short:     "Generate shell completion script",
		ValidArgs: []string{"bash", "zsh", "fish"},
		Args:      cobra.ExactValidArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				return rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				return rootCmd.GenFishCompletion(os.Stdout, true)
			}
			return nil
		},
	}
}
