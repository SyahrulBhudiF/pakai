package inferhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dhanifudin/pakai/internal/schema"
)

const okPayload = `{
	"object": "account.usage",
	"currency": "USDC",
	"balance": { "amount_usdc": "12.500000", "updated_at": "2026-08-08T12:34:56Z" },
	"window": {
		"kind": "30d",
		"since": "2026-07-09T00:00:00Z",
		"until": "2026-08-08T00:00:00Z",
		"spend_usdc": "5.250000"
	}
}`

func TestParseUsage(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    accountUsage
		wantErr bool
	}{
		{
			name:    "ok",
			payload: okPayload,
			want: accountUsage{
				Balance: 12.5,
				Spend:   5.25,
				Since:   time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
				Until:   time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "bad spend",
			payload: `{
				"balance": { "amount_usdc": "12.500000" },
				"window": { "since": "2026-07-09T00:00:00Z", "until": "2026-08-08T00:00:00Z", "spend_usdc": "not-a-number" }
			}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp usageResponse
			if err := json.Unmarshal([]byte(tt.payload), &resp); err != nil {
				t.Fatal(err)
			}

			got, err := parseUsage(resp)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseUsage error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Balance != tt.want.Balance {
				t.Fatalf("Balance = %v, want %v", got.Balance, tt.want.Balance)
			}
			if got.Spend != tt.want.Spend {
				t.Fatalf("Spend = %v, want %v", got.Spend, tt.want.Spend)
			}
			if !got.Since.Equal(tt.want.Since) {
				t.Fatalf("Since = %v, want %v", got.Since, tt.want.Since)
			}
			if !got.Until.Equal(tt.want.Until) {
				t.Fatalf("Until = %v, want %v", got.Until, tt.want.Until)
			}
		})
	}
}

func TestWindows(t *testing.T) {
	a := accountUsage{
		Balance: 12.5,
		Spend:   5,
		Since:   time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
		Until:   time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
	}

	got := windows(a, 20)
	if len(got) != 2 {
		t.Fatalf("len(windows) = %d, want 2", len(got))
	}
	if got[0].Key != "30d" || got[0].Used != 5 || got[0].Limit != 20 || got[0].Unit != "usd" {
		t.Fatalf("windows[0] = %+v", got[0])
	}
	if !got[0].ResetAt.IsZero() {
		t.Fatalf("windows[0].ResetAt = %v, want zero because the window is rolling", got[0].ResetAt)
	}
	if got[1].Key != "bal" || got[1].Used != 12.5 || got[1].Limit != 0 {
		t.Fatalf("windows[1] = %+v", got[1])
	}
}

func TestFetchWindows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okPayload))
	}))
	defer server.Close()

	p := newProvider(server.URL, "test-key", "inferhub", 0)
	usage, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if usage.Provider != "inferhub" {
		t.Fatalf("Provider = %q, want %q", usage.Provider, "inferhub")
	}
	if usage.Status != schema.StatusOK {
		t.Fatalf("Status = %q, want %q", usage.Status, schema.StatusOK)
	}
	if len(usage.Windows) != 2 {
		t.Fatalf("len(Windows) = %d, want 2", len(usage.Windows))
	}
	if usage.Used != 12.5 {
		t.Fatalf("Used = %v, want 12.5", usage.Used)
	}
}

func TestFetchStatusErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{
			name:    "unauthorized",
			status:  http.StatusUnauthorized,
			body:    ``,
			wantErr: "InferHub API key is invalid",
		},
		{
			name:    "insufficient key scope",
			status:  http.StatusForbidden,
			body:    `{"error":{"code":"insufficient_key_scope","message":"key lacks usage scope"}}`,
			wantErr: "InferHub key cannot read usage, use an inference key",
		},
		{
			name:    "account blocked",
			status:  http.StatusForbidden,
			body:    `{"error":{"code":"account_blocked","message":"Account suspended by an administrator"}}`,
			wantErr: "Account suspended by an administrator",
		},
		{
			name:    "rate limited",
			status:  http.StatusTooManyRequests,
			body:    ``,
			wantErr: "InferHub rate limited, retrying",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			p := newProvider(server.URL, "test-key", "inferhub", 0)
			usage, err := p.Fetch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if usage.Status != schema.StatusError {
				t.Fatalf("Status = %q, want %q", usage.Status, schema.StatusError)
			}
			if usage.Error != tt.wantErr {
				t.Fatalf("Error = %q, want %q", usage.Error, tt.wantErr)
			}
		})
	}
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want bool
	}{
		{"no section", "", false},
		{"no key", "[provider.inferhub]\nenabled = true\n", false},
		{"key only", "[provider.inferhub]\nkey = \"sk-airo-test\"\n", true},
		{"key and enabled", "[provider.inferhub]\nkey = \"sk-airo-test\"\nenabled = true\n", true},
		{"key but disabled", "[provider.inferhub]\nkey = \"sk-airo-test\"\nenabled = false\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)

			configDir := filepath.Join(dir, "pakai")
			if err := os.MkdirAll(configDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(tt.toml), 0600); err != nil {
				t.Fatal(err)
			}

			if got := Enabled(); got != tt.want {
				t.Fatalf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
