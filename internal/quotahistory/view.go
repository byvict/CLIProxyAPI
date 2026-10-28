package quotahistory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// The history view contract and analysis retain the v1 renderer's data model.
type windowKey struct {
	Kind          string `json:"kind"`
	Scope         string `json:"scope"`
	PeriodSeconds int64  `json:"periodSeconds"`
	Unit          string `json:"unit"`
	CapacityBasis string `json:"capacityBasis"`
}

func (k windowKey) id() string {
	b, _ := json.Marshal(k)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

type windowReading struct {
	Key       windowKey  `json:"key"`
	Label     string     `json:"label"`
	Remaining *float64   `json:"remaining"`
	Capacity  float64    `json:"capacity"`
	ResetsAt  *time.Time `json:"resetsAt"`
}

type sample struct {
	At     time.Time     `json:"at"`
	Window windowReading `json:"window"`
}

type Credential struct {
	ID, Provider, Label string
	Active              bool
}

type historyWindow struct {
	windowReading
	ID         string    `json:"id"`
	Current    bool      `json:"current"`
	Freshness  string    `json:"freshness"`
	ObservedAt time.Time `json:"observedAt"`
}

type identityView struct {
	Label string `json:"label"`
}

type accountView struct {
	ID             string          `json:"id"`
	Provider       string          `json:"provider"`
	Identity       identityView    `json:"identity"`
	Active         bool            `json:"active"`
	ObservedAt     *time.Time      `json:"observedAt"`
	Windows        []historyWindow `json:"windows"`
	HistoryWindows []historyWindow `json:"historyWindows"`
}

type quotaTotal struct {
	Remaining     *float64 `json:"remaining"`
	Capacity      float64  `json:"capacity"`
	Contributors  []string `json:"contributors"`
	Missing       []string `json:"missing"`
	Normalization string   `json:"normalization"`
}

type providerWindow struct {
	Key       windowKey  `json:"key"`
	Label     string     `json:"label"`
	Total     quotaTotal `json:"total"`
	NextReset *time.Time `json:"nextReset"`
}

type providerView struct {
	Provider     string           `json:"provider"`
	AccountCount int              `json:"accountCount"`
	LoadedCount  int              `json:"loadedCount"`
	Windows      []providerWindow `json:"windows"`
}

type Snapshot struct {
	GeneratedAt time.Time      `json:"generatedAt"`
	Accounts    []accountView  `json:"accounts"`
	Providers   []providerView `json:"providers"`
	Sources     []any          `json:"sources"`
}

type accountAnalysis struct {
	analysis
	AccountID string `json:"accountId"`
	WindowID  string `json:"windowId"`
	Label     string `json:"label"`
	Freshness string `json:"freshness"`
}

type historyGroup struct {
	Key      windowKey         `json:"key"`
	Label    string            `json:"label"`
	Total    quotaTotal        `json:"total"`
	Accounts []accountAnalysis `json:"accounts"`
}

type View struct {
	GeneratedAt       time.Time      `json:"generatedAt"`
	SelectionRequired string         `json:"selectionRequired"`
	Groups            []historyGroup `json:"groups"`
}

func providerName(provider string) string {
	switch provider {
	case "meta":
		return "muse"
	case "xai":
		return "grok"
	default:
		return provider
	}
}

func historyKey(o Observation) windowKey {
	key := windowKey{Kind: "other", Scope: "all", Unit: "percent", CapacityBasis: "account-percent:" + o.Credential}
	if o.PeriodHours != nil {
		key.PeriodSeconds = int64(*o.PeriodHours * 3600)
		if *o.PeriodHours <= 5 {
			key.Kind = "session"
		} else if *o.PeriodHours == 168 {
			key.Kind = "weekly"
		}
	}
	switch o.WindowID {
	case "weekly", "seven-day":
		key.Kind = "weekly"
	case "five-hour", "window":
		key.Kind = "session"
	case "seven-day-fable":
		key.Kind, key.Scope = "weekly", "provider-label:Fable"
	default:
		key.Scope = o.WindowID
	}
	return key
}

func asSample(o Observation) sample {
	w := windowReading{Key: historyKey(o), Label: o.Label, Remaining: o.Remaining, Capacity: 100}
	if o.ResetAtMs != nil {
		reset := time.UnixMilli(*o.ResetAtMs)
		w.ResetsAt = &reset
	}
	return sample{At: time.UnixMilli(o.ObservedAtMs), Window: w}
}

func cadenceFor(provider string) time.Duration {
	if provider == "muse" || provider == "meta" {
		return 30 * time.Minute
	}
	return 5 * time.Minute
}

func maxGapFor(provider string) time.Duration {
	if provider == "muse" || provider == "meta" {
		return 65 * time.Minute
	}
	return 45 * time.Minute
}

func MakeSnapshot(observations []Observation, credentials []Credential, now time.Time) Snapshot {
	accounts := map[string]*accountView{}
	for _, credential := range credentials {
		accounts[credential.ID] = &accountView{ID: credential.ID, Provider: providerName(credential.Provider), Identity: identityView{Label: credential.Label}, Active: credential.Active, Windows: []historyWindow{}, HistoryWindows: []historyWindow{}}
	}
	latest := map[string]map[string]sample{}
	for _, observation := range observations {
		if observation.ObservedAtMs > now.UnixMilli() {
			continue
		}
		account := accounts[observation.Credential]
		if account == nil {
			account = &accountView{ID: observation.Credential, Provider: providerName(observation.Provider), Identity: identityView{Label: observation.Provider + " · " + observation.Credential}, Windows: []historyWindow{}, HistoryWindows: []historyWindow{}}
			if strings.HasPrefix(observation.Credential, "codexbar:") {
				account.Identity.Label = observation.Provider + " · CodexBar " + strings.TrimPrefix(observation.Credential, "codexbar:") + " · conta histórica"
			}
			if strings.HasPrefix(observation.Credential, "v1-account:") {
				account.Identity.Label = observation.Provider + " · Monitor V1 " + strings.TrimPrefix(observation.Credential, "v1-account:") + " · conta histórica"
			}
			accounts[account.ID] = account
		}
		if latest[account.ID] == nil {
			latest[account.ID] = map[string]sample{}
		}
		s := asSample(observation)
		id := s.Window.Key.id()
		if old, found := latest[account.ID][id]; !found || s.At.After(old.At) {
			latest[account.ID][id] = s
		}
	}
	result := Snapshot{GeneratedAt: now.In(saoPaulo), Accounts: []accountView{}, Providers: []providerView{}, Sources: []any{}}
	for _, account := range accounts {
		for _, s := range latest[account.ID] {
			freshness := "fresh"
			if now.Sub(s.At) > maxGapFor(account.Provider) {
				freshness = "stale"
			}
			w := historyWindow{windowReading: s.Window, ID: s.Window.Key.id(), Current: account.Active && !strings.HasPrefix(s.Window.Key.Scope, "v1:"), Freshness: freshness, ObservedAt: s.At}
			account.HistoryWindows = append(account.HistoryWindows, w)
			if !strings.HasPrefix(s.Window.Key.Scope, "v1:") {
				account.Windows = append(account.Windows, w)
			}
			if account.ObservedAt == nil || s.At.After(*account.ObservedAt) {
				at := s.At
				account.ObservedAt = &at
			}
		}
		sort.Slice(account.Windows, func(i, j int) bool { return account.Windows[i].ID < account.Windows[j].ID })
		sort.Slice(account.HistoryWindows, func(i, j int) bool { return account.HistoryWindows[i].ID < account.HistoryWindows[j].ID })
		result.Accounts = append(result.Accounts, *account)
	}
	sort.Slice(result.Accounts, func(i, j int) bool { return result.Accounts[i].ID < result.Accounts[j].ID })
	providers := map[string]*providerView{}
	for _, account := range result.Accounts {
		p := providers[account.Provider]
		if p == nil {
			p = &providerView{Provider: account.Provider, Windows: []providerWindow{}}
			providers[account.Provider] = p
		}
		if account.Active {
			p.AccountCount++
		}
		if account.Active && account.ObservedAt != nil {
			p.LoadedCount++
		}
		for _, window := range account.Windows {
			if !account.Active {
				continue
			}
			total := totalFor(account.ID, window.Remaining)
			p.Windows = append(p.Windows, providerWindow{Key: window.Key, Label: window.Label, Total: total, NextReset: window.ResetsAt})
		}
	}
	for _, p := range providers {
		result.Providers = append(result.Providers, *p)
	}
	sort.Slice(result.Providers, func(i, j int) bool { return result.Providers[i].Provider < result.Providers[j].Provider })
	return result
}

func totalFor(account string, remaining *float64) quotaTotal {
	total := quotaTotal{Remaining: remaining, Capacity: 100, Contributors: []string{}, Missing: []string{}, Normalization: "Percentual desta conta; janelas e planos distintos permanecem separados."}
	if remaining == nil {
		total.Missing = append(total.Missing, account)
	} else {
		total.Contributors = append(total.Contributors, account)
	}
	return total
}

func MakeView(observations []Observation, snapshot Snapshot, provider, accountID, windowID string, period time.Duration, now time.Time) View {
	view := View{GeneratedAt: now.In(saoPaulo), Groups: []historyGroup{}}
	if provider == "all" || provider == "" {
		view.SelectionRequired = "Selecione um provedor para consultar o histórico de suas contas e janelas."
		return view
	}
	for _, account := range snapshot.Accounts {
		if account.Provider != provider || (accountID != "" && account.ID != accountID) || (accountID == "" && !account.Active) {
			continue
		}
		windows := account.Windows
		if accountID != "" {
			windows = account.HistoryWindows
		}
		for _, window := range windows {
			if window.ID != windowID {
				continue
			}
			samples := []sample{}
			for _, observation := range observations {
				if observation.Credential == account.ID {
					s := asSample(observation)
					if s.Window.Key == window.Key {
						samples = append(samples, s)
					}
				}
			}
			result := analyze(samples, analysisOptions{Now: now, Cadence: cadenceFor(provider), MaxGap: maxGapFor(provider), Period: period, Lookback: time.Hour})
			view.Groups = append(view.Groups, historyGroup{Key: window.Key, Label: window.Label, Total: totalFor(account.ID, window.Remaining), Accounts: []accountAnalysis{{analysis: result, AccountID: account.ID, WindowID: window.ID, Label: account.Identity.Label, Freshness: window.Freshness}}})
		}
	}
	return view
}
