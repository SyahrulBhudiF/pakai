package inferhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dhanifudin/pakai/internal/config"
	"github.com/dhanifudin/pakai/internal/schema"
)

const (
	providerID  = "inferhub"
	usageURL    = "https://api.inferhub.dev/v1/me/usage"
	spendWindow = "30d"
)

type Provider struct {
	usageURL   string
	apiKey     string
	label      string
	budget     float64
	httpClient *http.Client
	now        func() time.Time
}

func New() *Provider {
	return newProvider(usageURL, config.GetProviderKey(providerID), config.GetProviderLabel(providerID), config.GetProviderLimit(providerID))
}

func newProvider(url, apiKey, label string, budget float64) *Provider {
	return &Provider{
		usageURL:   url,
		apiKey:     apiKey,
		label:      label,
		budget:     budget,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		now:        time.Now,
	}
}

// Enabled reports whether InferHub should be polled. InferHub needs an API key,
// so it stays off until provider.inferhub.key is set.
func Enabled() bool {
	return config.GetProviderKey(providerID) != "" && config.IsProviderEnabled(providerID)
}

func (p *Provider) ID() string {
	return providerID
}

func (p *Provider) UsageURL() string {
	return p.usageURL
}

type usageResponse struct {
	Balance struct {
		AmountUSDC string `json:"amount_usdc"`
	} `json:"balance"`
	Window struct {
		Since     time.Time `json:"since"`
		Until     time.Time `json:"until"`
		SpendUSDC string    `json:"spend_usdc"`
	} `json:"window"`
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type accountUsage struct {
	Balance float64
	Spend   float64
	Since   time.Time
	Until   time.Time
}

func parseUsage(r usageResponse) (accountUsage, error) {
	balance, err := strconv.ParseFloat(r.Balance.AmountUSDC, 64)
	if err != nil {
		return accountUsage{}, fmt.Errorf("parse InferHub balance %q: %w", r.Balance.AmountUSDC, err)
	}
	spend, err := strconv.ParseFloat(r.Window.SpendUSDC, 64)
	if err != nil {
		return accountUsage{}, fmt.Errorf("parse InferHub spend %q: %w", r.Window.SpendUSDC, err)
	}
	return accountUsage{
		Balance: balance,
		Spend:   spend,
		Since:   r.Window.Since,
		Until:   r.Window.Until,
	}, nil
}

// windows builds the spend window and the balance window. ResetAt is left zero
// on the spend window because InferHub reports a rolling window whose end is
// always the current time, so a reset timestamp would render as "resets now".
func windows(a accountUsage, budget float64) []schema.UsageWindow {
	return []schema.UsageWindow{
		{
			Key:         spendWindow,
			Label:       spendWindow,
			PeriodStart: a.Since,
			PeriodEnd:   a.Until,
			Used:        a.Spend,
			Limit:       budget,
			Unit:        "usd",
		},
		{
			Key:   "bal",
			Label: "bal",
			Used:  a.Balance,
			Unit:  "usd",
		},
	}
}

func (p *Provider) Fetch(ctx context.Context) (*schema.Usage, error) {
	a, err := p.fetchUsage(ctx)
	if err != nil {
		return p.errorUsage(err), nil
	}

	return &schema.Usage{
		Provider:    providerID,
		Label:       p.label,
		PeriodStart: a.Since,
		PeriodEnd:   a.Until,
		Used:        a.Balance,
		Unit:        "usd",
		Windows:     windows(a, p.budget),
		Status:      schema.StatusOK,
		RefreshedAt: p.now(),
	}, nil
}

func (p *Provider) errorUsage(err error) *schema.Usage {
	return &schema.Usage{
		Provider:    providerID,
		Label:       p.label,
		Status:      schema.StatusError,
		Error:       err.Error(),
		RefreshedAt: p.now(),
	}
}

func (p *Provider) fetchUsage(ctx context.Context) (accountUsage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.usageURL+"?window="+spendWindow, nil)
	if err != nil {
		return accountUsage{}, fmt.Errorf("create InferHub usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return accountUsage{}, fmt.Errorf("fetch InferHub usage: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return accountUsage{}, errors.New("InferHub API key is invalid")
	case http.StatusForbidden:
		var er errorResponse
		if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
			return accountUsage{}, fmt.Errorf("InferHub rejected the key: status %d", resp.StatusCode)
		}
		switch er.Error.Code {
		case "insufficient_key_scope":
			return accountUsage{}, errors.New("InferHub key cannot read usage, use an inference key")
		case "account_blocked":
			if er.Error.Message != "" {
				return accountUsage{}, errors.New(er.Error.Message)
			}
			return accountUsage{}, errors.New("InferHub account is blocked")
		}
		return accountUsage{}, fmt.Errorf("InferHub rejected the key: status %d", resp.StatusCode)
	case http.StatusTooManyRequests:
		return accountUsage{}, errors.New("InferHub rate limited, retrying")
	default:
		return accountUsage{}, fmt.Errorf("InferHub usage request failed: status %d", resp.StatusCode)
	}

	var ur usageResponse
	if err := json.NewDecoder(resp.Body).Decode(&ur); err != nil {
		return accountUsage{}, fmt.Errorf("parse InferHub usage: %w", err)
	}
	return parseUsage(ur)
}
