package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dhanifudin/pakai/internal/cache"
	"github.com/dhanifudin/pakai/internal/config"
	"github.com/dhanifudin/pakai/internal/providers"
	"github.com/dhanifudin/pakai/internal/providers/claude"
	"github.com/dhanifudin/pakai/internal/providers/codex"
	inferhubprovider "github.com/dhanifudin/pakai/internal/providers/inferhub"
	"github.com/dhanifudin/pakai/internal/providers/mock"
	"github.com/dhanifudin/pakai/internal/providers/opencode"
	"github.com/dhanifudin/pakai/internal/providers/opencodego"
	piprovider "github.com/dhanifudin/pakai/internal/providers/pi"
	"github.com/dhanifudin/pakai/internal/schema"
)

// Server is the HTTP daemon server.
type Server struct {
	port        int
	startTime   time.Time
	version     string
	binaryMtime time.Time
	loop        *RefreshLoop
	cache       *cache.Cache
	hub         *Hub
	watcher     *config.Watcher

	httpServer *http.Server
	pidFile    string
}

// NewServer creates a new daemon server.
func NewServer(port int, version string) (*Server, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	cacheDir := filepath.Join(home, ".cache", "pakai")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, err
	}

	c := cache.New(filepath.Join(cacheDir, "cache.json"))

	pidFile := pidFilePath()
	if err := ensurePIDDir(); err != nil {
		return nil, fmt.Errorf("failed to create PID directory: %w", err)
	}

	// Handle stale PID file
	if data, err := os.ReadFile(pidFile); err == nil {
		pid, parseErr := readPIDFile(pidFile)
		if parseErr == nil && !isProcessAlive(pid) {
			os.Remove(pidFile)
		} else if parseErr != nil {
			os.Remove(pidFile)
		}
		_ = data
	}

	h := NewHub()

	var binaryMtime time.Time
	if exe, err := os.Executable(); err == nil {
		if st, err := os.Stat(exe); err == nil {
			binaryMtime = st.ModTime()
		}
	}

	s := &Server{
		port:        port,
		startTime:   time.Now(),
		version:     version,
		binaryMtime: binaryMtime,
		cache:       c,
		hub:         h,
		pidFile:     pidFile,
		watcher:     config.NewWatcher(),
	}

	provs := s.buildProviders()
	s.loop = NewRefreshLoop(provs, c, s.broadcastViaHub)

	return s, nil
}

// buildProviders builds the list of providers to use.
func (s *Server) buildProviders() []providers.Provider {
	var provs []providers.Provider

	// Check for mock providers first
	mockedIDs, _ := mock.ListMocked()
	mockedSet := make(map[string]bool)
	for _, id := range mockedIDs {
		mp, err := mock.Load(id)
		if err == nil {
			provs = append(provs, mp)
			mockedSet[id] = true
		}
	}

	// Add real providers if enabled and not mocked
	if config.IsProviderEnabled("claude") && !mockedSet["claude"] {
		provs = append(provs, claude.New())
	}

	if config.IsProviderEnabled("opencode") && !mockedSet["opencode"] {
		provs = append(provs, opencode.New())
	}

	if config.IsProviderEnabled("openai") && !mockedSet["openai"] {
		provs = append(provs, codex.New())
	}

	if config.IsProviderEnabled("opencode-go") && !mockedSet["opencode-go"] {
		provs = append(provs, opencodego.New())
	}

	if config.IsProviderEnabled("pi") && !mockedSet["pi"] {
		provs = append(provs, piprovider.New())
	}

	if inferhubprovider.Enabled() && !mockedSet["inferhub"] {
		provs = append(provs, inferhubprovider.New())
	}
	return filterHiddenProviders(provs)
}

// Start starts the daemon server.
func (s *Server) Start() error {
	// Port conflict detection
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			return fmt.Errorf("port %d is already in use by another process", s.port)
		}
		return fmt.Errorf("failed to bind to %s: %w", addr, err)
	}
	l.Close()

	// Write PID file
	pid := os.Getpid()
	if err := os.WriteFile(s.pidFile, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer os.Remove(s.pidFile)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("PUT /mock/{name}", s.handleMockPut)
	mux.HandleFunc("DELETE /mock/{name}", s.handleMockDelete)
	mux.HandleFunc("GET /debug/{name}", s.handleDebug)
	mux.HandleFunc("GET /api/config", s.handleWidgetConfig)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start the refresh loop
	s.loop.Start()

	// Start config file watcher
	s.watcher.Start()

	log.Printf("pakai daemon starting on 127.0.0.1:%d", s.port)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func isAddrInUse(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if sysErr, ok := opErr.Err.(*os.SyscallError); ok {
			return sysErr.Err == syscall.EADDRINUSE
		}
	}
	return false
}

// -- Mock handlers --

type mockPayload struct {
	Percent *float64 `json:"percent"`
	Cost    *float64 `json:"cost"`
}

func (s *Server) handleMockPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var payload mockPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || (payload.Percent == nil && payload.Cost == nil) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid mock payload"})
		return
	}

	now := time.Now()
	u := &schema.Usage{
		Provider:    name,
		Label:       name,
		Status:      schema.StatusOK,
		RefreshedAt: now,
	}

	if payload.Percent != nil {
		u.Unit = "percent"
		u.Limit = 100
		u.Used = *payload.Percent
		u.Status = schema.StatusMock
	}
	if payload.Cost != nil {
		u.Unit = "usd"
		u.Used = *payload.Cost
		if payload.Percent == nil {
			u.Status = schema.StatusMock
		}
	}

	s.storeMock(name, u)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleMockDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if !s.removeMock(name) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "no mock active for provider: " + name})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) storeMock(name string, u *schema.Usage) {
	s.loop.mu.Lock()
	defer s.loop.mu.Unlock()
	current := s.loop.current
	replaced := false
	for i, existing := range current {
		if existing.Provider == name {
			current[i] = u
			replaced = true
			break
		}
	}
	if !replaced {
		current = append(current, u)
	}
	s.loop.current = current
}

func (s *Server) removeMock(name string) bool {
	s.loop.mu.Lock()
	defer s.loop.mu.Unlock()
	current := s.loop.current
	found := false
	for i, existing := range current {
		if existing.Provider == name && existing.Status == schema.StatusMock {
			current = append(current[:i], current[i+1:]...)
			found = true
			break
		}
	}
	s.loop.current = current
	return found
}

// -- Debug handler --

func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	usages := s.loop.Current()
	var target *schema.Usage
	for _, u := range usages {
		if u.Provider == name {
			target = u
			break
		}
	}

	if target == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "unknown provider: " + name})
		return
	}

	info := map[string]interface{}{
		"provider": target.Provider,
		"label":    target.Label,
		"state":    string(target.Status),
		"used":     target.Used,
		"limit":    target.Limit,
		"unit":     target.Unit,
		"error":    target.Error,
	}

	if !target.RefreshedAt.IsZero() {
		info["refreshed_at"] = target.RefreshedAt.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.loop.Stop()
	s.watcher.Stop()
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			return err
		}
	}
	s.hub.Wait()
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	connections := int(s.hub.clientCnt.Load())

	uptimeSeconds := int(time.Since(s.startTime).Seconds())

	providers := make([]string, 0)
	if s.loop != nil {
		s.loop.mu.RLock()
		for _, p := range s.loop.providers {
			providers = append(providers, p.ID())
		}
		s.loop.mu.RUnlock()
	}

	resp := map[string]interface{}{
		"status":         "ok",
		"uptime_seconds": uptimeSeconds,
		"connections":    connections,
		"port":           s.port,
		"providers":      providers,
		"version":        s.version,
		"binary_mtime":   s.binaryMtime,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	data := s.loop.Current()
	w.Header().Set("Content-Type", "application/json")
	if data == nil {
		// Try reading from cache
		entry, _ := s.cache.Read()
		if entry != nil {
			data = entry.Data
		}
	}
	if data == nil {
		data = []*schema.Usage{}
	}
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.hub.SetSnapshot(s.loop.Current())
	s.hub.Subscribe(w, r)
}

func (s *Server) broadcastViaHub(data []*schema.Usage) {
	s.hub.Broadcast(data)
}

// IsRunning checks if the daemon is running by connecting to its health endpoint.
func IsRunning(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetPID reads the daemon PID from the PID file.
func GetPID() (int, error) {
	pidFile := pidFilePath()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, fmt.Errorf("daemon not running (no PID file)")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID file: %w", err)
	}
	return pid, nil
}

// -- Widget config API handler (query-param based) --

type widgetConfigResponse struct {
	Widget    config.WidgetConfig `json:"widget"`
	Providers map[string]bool     `json:"providers"`
	Sources   map[string]string   `json:"sources"`
}

func (s *Server) handleWidgetConfig(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mutated := false

	if hide := q.Get("hide"); hide != "" {
		cfg := config.Config()
		if !slices.Contains(cfg.Widget.Hidden, hide) {
			hidden := append([]string(nil), cfg.Widget.Hidden...)
			hidden = append(hidden, hide)
			if err := config.SetWidgetHiddenList(hidden); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			mutated = true
		}
	}

	if unhide := q.Get("unhide"); unhide != "" {
		cfg := config.Config()
		hidden := make([]string, 0, len(cfg.Widget.Hidden))
		for _, id := range cfg.Widget.Hidden {
			if id != unhide {
				hidden = append(hidden, id)
			}
		}
		if len(hidden) != len(cfg.Widget.Hidden) {
			if err := config.SetWidgetHiddenList(hidden); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			mutated = true
		}
	}

	if q.Has("pin") {
		if err := config.SetWidgetPinned(q.Get("pin")); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		mutated = true
	}

	if q.Has("unpin") {
		if err := config.SetWidgetPinned(""); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		mutated = true
	}

	if id := q.Get("enable"); id != "" {
		if err := config.SetKey("provider."+id+".enabled", "true"); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}
		mutated = true
	}

	if id := q.Get("disable"); id != "" {
		if err := config.SetKey("provider."+id+".enabled", "false"); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}
		mutated = true
	}

	if id, source := q.Get("source"), q.Get("value"); id != "" && source != "" {
		if err := config.SetKey("provider."+id+".source", source); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}
		mutated = true
	}

	if mutated {
		s.loop.UpdateProviders(s.buildProviders())
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(widgetConfigResponse{
		Widget: config.Config().Widget,
		Providers: map[string]bool{
			"claude":      config.IsProviderEnabled("claude"),
			"openai":      config.IsProviderEnabled("openai"),
			"opencode":    config.IsProviderEnabled("opencode"),
			"opencode-go": config.IsProviderEnabled("opencode-go"),
			"pi":          config.IsProviderEnabled("pi"),
			"inferhub":    config.IsProviderEnabled("inferhub"),
		},
		Sources: map[string]string{
			"openai":      config.GetProviderSource("openai"),
			"opencode-go": config.GetProviderSource("opencode-go"),
		},
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	s.loop.RefreshNow()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// filterHiddenProviders removes any providers that are in the widget hidden config.
func filterHiddenProviders(provs []providers.Provider) []providers.Provider {
	hidden := config.Config().Widget.Hidden
	if len(hidden) == 0 {
		return provs
	}
	hiddenSet := make(map[string]bool, len(hidden))
	for _, id := range hidden {
		hiddenSet[id] = true
	}
	filtered := make([]providers.Provider, 0, len(provs))
	for _, p := range provs {
		if !hiddenSet[p.ID()] {
			filtered = append(filtered, p)
		}
	}
	return filtered
}
