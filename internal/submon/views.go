package submon

import (
	"errors"
	"sort"
	"time"
)

type identityView struct {
	Basis    identityBasis `json:"basis"`
	Label    string        `json:"label"`
	HasEmail bool          `json:"hasEmail"`
}
type windowView struct {
	windowReading
	ID             string     `json:"id"`
	ObservedAt     *time.Time `json:"observedAt"`
	AgeSeconds     *float64   `json:"ageSeconds"`
	Freshness      string     `json:"freshness"`
	PacePpH        *float64   `json:"pacePpH"`
	SustainablePpH *float64   `json:"sustainablePpH"`
	ExhaustsAt     *time.Time `json:"exhaustsAt"`
}
type accountView struct {
	ID           string       `json:"id"`
	Provider     provider     `json:"provider"`
	Identity     identityView `json:"identity"`
	Active       bool         `json:"active"`
	Sources      []string     `json:"sources"`
	Status       string       `json:"status"`
	Message      string       `json:"message"`
	Freshness    string       `json:"freshness"`
	ObservedAt   *time.Time   `json:"observedAt"`
	AgeSeconds   *float64     `json:"ageSeconds"`
	Windows      []windowView `json:"windows"`
	Availability availability `json:"availability"`
	ResetCredits *int         `json:"resetCredits"`
	ResetAction  struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	} `json:"resetAction"`
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
	Provider      provider         `json:"provider"`
	AccountCount  int              `json:"accountCount"`
	RetainedCount int              `json:"retainedCount"`
	LoadedCount   int              `json:"loadedCount"`
	Windows       []providerWindow `json:"windows"`
}
type sourceView struct {
	ID               string     `json:"id"`
	Provider         provider   `json:"provider"`
	Status           status     `json:"status"`
	Message          string     `json:"message"`
	LastAttemptAt    *time.Time `json:"lastAttemptAt"`
	CurrentAccountID string     `json:"currentAccountId"`
	workerState
}
type snapshot struct {
	GeneratedAt time.Time      `json:"generatedAt"`
	Timezone    string         `json:"timezone"`
	Accounts    []accountView  `json:"accounts"`
	Providers   []providerView `json:"providers"`
	Sources     []sourceView   `json:"sources"`
}

func historyFor(data storedData, account string, key windowKey) []sample {
	byAttempt := map[string]sample{}
	for _, s := range data.Samples {
		if s.AccountID == account && s.Window.Key == key {
			byAttempt[s.AttemptID] = s.sample
		}
	}
	byTime := map[int64]sample{}
	for _, a := range data.Attempts {
		belongs := a.AccountID == account
		if a.AccountID == "" {
			for _, b := range data.Bindings {
				if b.AccountID == account && b.SourceID == a.SourceID && !a.At.Before(b.From) && (b.To == nil || !a.At.After(*b.To)) {
					belongs = true
					break
				}
			}
		}
		if !belongs {
			continue
		}
		s, found := byAttempt[a.ID]
		if !found {
			s = sample{At: a.At, Window: windowReading{Key: key}}
		}
		old, exists := byTime[s.At.UnixNano()]
		if !exists || old.Window.Remaining == nil {
			byTime[s.At.UnixNano()] = s
		}
	}
	out := make([]sample, 0, len(byTime))
	for _, s := range byTime {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
func currentWindows(data storedData, id string) []windowKey {
	lastAttempt := ""
	for _, a := range data.Attempts {
		if a.AccountID == id && a.Status == ok {
			lastAttempt = a.ID
		}
	}
	out := []windowKey{}
	for _, s := range data.Samples {
		if s.AccountID == id && s.AttemptID == lastAttempt {
			out = append(out, s.Window.Key)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Scope < out[j].Scope
	})
	return out
}
func makeSnapshot(data storedData, states map[string]workerState, now time.Time) snapshot {
	out := snapshot{GeneratedAt: now.In(saoPaulo), Timezone: "America/Sao_Paulo", Accounts: []accountView{}, Providers: []providerView{}, Sources: []sourceView{}}
	for _, p := range providers {
		id := string(p) + "-native"
		src := sourceView{ID: id, Provider: p, Status: unavailable, workerState: states[id]}
		if src.NextAt.IsZero() {
			src.NextAt = data.Sources[id].NextAt
			src.Failures = data.Sources[id].Failures
		}
		for _, b := range data.Bindings {
			if b.SourceID == id && b.To == nil {
				src.CurrentAccountID = b.AccountID
			}
		}
		for _, a := range data.Attempts {
			if a.SourceID == id {
				at := a.At
				src.LastAttemptAt = &at
				src.Status = a.Status
				src.Message = statusMessage(a.Status, a.Code)
			}
		}
		if src.Message == "" {
			src.Message = "Aguardando a primeira leitura da fonte nativa."
		}
		out.Sources = append(out.Sources, src)
	}
	for _, a := range data.Accounts {
		av := accountView{ID: a.ID, Provider: a.Provider, Identity: identityView{Basis: a.Basis, Label: a.Label, HasEmail: a.Email != ""}, Status: "signed_out", Freshness: "missing", Sources: []string{}, Windows: []windowView{}}
		av.ResetAction.Reason = "A CLI nativa não informa créditos nem uma ação de reset elegível."
		for _, b := range data.Bindings {
			if b.AccountID == a.ID && b.To == nil {
				av.Active = true
				av.Sources = append(av.Sources, b.SourceID)
			}
		}
		if av.Active {
			av.Status = string(ok)
			for _, src := range out.Sources {
				if src.CurrentAccountID == a.ID && src.Status != ok {
					av.Status = string(src.Status)
					av.Message = src.Message
				}
			}
		} else {
			av.Message = "Fora de sessão. Histórico preservado."
		}
		keys := currentWindows(data, a.ID)
		for _, key := range keys {
			h := historyFor(data, a.ID, key)
			w := windowView{ID: key.id(), Freshness: "missing"}
			var latest *sample
			for i := range h {
				if h[i].Window.Remaining != nil {
					latest = &h[i]
				}
			}
			if latest != nil {
				w.windowReading = latest.Window
				at := latest.At
				w.ObservedAt = &at
				age := now.Sub(at).Seconds()
				if age < 0 {
					age = 0
				}
				w.AgeSeconds = &age
				w.Freshness = "fresh"
				if time.Duration(age*float64(time.Second)) > 2*cadence(a.Provider) || av.Status != string(ok) || h[len(h)-1].Window.Remaining == nil {
					w.Freshness = "stale"
				}
			} else {
				for _, s := range h {
					if s.Window.Capacity > 0 {
						w.windowReading = s.Window
					}
				}
			}
			if w.Key.Kind == "" {
				w.Key = key
			}
			analysis := analyze(h, analysisOptions{Now: now, Cadence: cadence(a.Provider), Period: 24 * time.Hour, Lookback: time.Hour})
			if w.Freshness == "fresh" {
				w.PacePpH = analysis.RecentPpH
				w.SustainablePpH = analysis.SustainablePpH
				w.ExhaustsAt = analysis.ExhaustsAt
			}
			av.Windows = append(av.Windows, w)
			if w.ObservedAt != nil && (av.ObservedAt == nil || w.ObservedAt.Before(*av.ObservedAt)) {
				av.ObservedAt = w.ObservedAt
				av.AgeSeconds = w.AgeSeconds
			}
		}
		if len(av.Windows) > 0 {
			av.Freshness = "fresh"
			for _, w := range av.Windows {
				if w.Freshness != "fresh" {
					av.Freshness = w.Freshness
					break
				}
			}
		}
		ws := []windowReading{}
		for _, w := range av.Windows {
			ws = append(ws, w.windowReading)
		}
		av.Availability = availabilityOf(ws)
		if av.Freshness != "fresh" || !av.Active {
			av.Availability.State = "unknown"
		}
		out.Accounts = append(out.Accounts, av)
	}
	for _, p := range providers {
		out.Providers = append(out.Providers, summarizeProvider(p, out.Accounts))
	}
	return out
}
func summarizeProvider(p provider, accounts []accountView) providerView {
	out := providerView{Provider: p, Windows: []providerWindow{}}
	groups := map[windowKey]*providerWindow{}
	for _, a := range accounts {
		if a.Provider != p {
			continue
		}
		out.RetainedCount++
		if !a.Active {
			continue
		}
		out.AccountCount++
		if a.ObservedAt != nil {
			out.LoadedCount++
		}
		for _, w := range a.Windows {
			g := groups[w.Key]
			if g == nil {
				g = &providerWindow{Key: w.Key, Label: w.Label, Total: quotaTotal{Contributors: []string{}, Missing: []string{}, Normalization: "Soma de percentuais normalizados. Planos sem capacidade comum não são equivalentes."}}
				groups[w.Key] = g
			}
			if w.Remaining != nil {
				v := *w.Remaining
				if g.Total.Remaining != nil {
					v += *g.Total.Remaining
				}
				g.Total.Remaining = &v
				g.Total.Capacity += w.Capacity
				g.Total.Contributors = append(g.Total.Contributors, a.ID)
			} else {
				g.Total.Missing = append(g.Total.Missing, a.ID)
			}
			if w.ResetsAt != nil && (g.NextReset == nil || w.ResetsAt.Before(*g.NextReset)) {
				g.NextReset = w.ResetsAt
			}
		}
	}
	for _, g := range groups {
		for _, a := range accounts {
			if a.Provider != p || !a.Active {
				continue
			}
			found := false
			for _, id := range append(append([]string{}, g.Total.Contributors...), g.Total.Missing...) {
				if id == a.ID {
					found = true
				}
			}
			if !found {
				g.Total.Missing = append(g.Total.Missing, a.ID)
			}
		}
		out.Windows = append(out.Windows, *g)
	}
	sort.Slice(out.Windows, func(i, j int) bool { return out.Windows[i].Key.id() < out.Windows[j].Key.id() })
	return out
}

type viewSelection struct {
	Provider   string        `json:"provider"`
	Account    string        `json:"account"`
	Window     string        `json:"window"`
	Period     time.Duration `json:"period"`
	Lookback   time.Duration `json:"lookback"`
	Horizon    time.Duration `json:"horizon"`
	Recurrence bool          `json:"recurrence"`
}
type accountAnalysis struct {
	WindowID  string `json:"windowId"`
	AccountID string `json:"accountId"`
	Label     string `json:"label"`
	Freshness string `json:"freshness"`
	analysis
}
type viewGroup struct {
	Key      windowKey         `json:"key"`
	Label    string            `json:"label"`
	Total    quotaTotal        `json:"total"`
	Accounts []accountAnalysis `json:"accounts"`
}
type view struct {
	GeneratedAt       time.Time      `json:"generatedAt"`
	Selection         viewSelection  `json:"selection"`
	SelectionRequired string         `json:"selectionRequired"`
	Providers         []providerView `json:"providers"`
	Groups            []viewGroup    `json:"groups"`
	Assumptions       []string       `json:"assumptions"`
}

func makeView(data storedData, states map[string]workerState, now time.Time, selection viewSelection) (view, error) {
	snap := makeSnapshot(data, states, now)
	out := view{GeneratedAt: now, Selection: selection, Groups: []viewGroup{}, Providers: snap.Providers, Assumptions: []string{"Ritmos observados entre amostras, em pontos percentuais por hora.", "Intervalos que cruzam horas ou dias permanecem não atribuídos.", "O primeiro reset usa a data informada; recorrências posteriores são inferidas.", "Percentuais de janelas e provedores diferentes não são somados."}}
	if selection.Provider == "all" || selection.Provider == "" {
		out.SelectionRequired = "Selecione um provedor e uma janela para analisar. Os provedores permanecem separados."
		return out, nil
	}
	validProvider := false
	for _, p := range providers {
		if selection.Provider == string(p) {
			validProvider = true
		}
	}
	if !validProvider {
		return out, errors.New("invalid provider")
	}
	if selection.Account != "" {
		found := false
		for _, a := range snap.Accounts {
			if a.ID == selection.Account && string(a.Provider) == selection.Provider {
				found = true
			}
		}
		if !found {
			return out, errors.New("invalid account")
		}
	}
	groups := map[windowKey]*viewGroup{}
	for _, a := range snap.Accounts {
		if string(a.Provider) != selection.Provider || (selection.Account != "" && a.ID != selection.Account) || (selection.Account == "" && !a.Active) {
			continue
		}
		keys := currentWindows(data, a.ID)
		if selection.Account != "" {
			seen := map[windowKey]bool{}
			for _, key := range keys {
				seen[key] = true
			}
			for _, s := range data.Samples {
				if s.AccountID == a.ID && !seen[s.Window.Key] {
					keys = append(keys, s.Window.Key)
					seen[s.Window.Key] = true
				}
			}
		}
		for _, key := range keys {
			if key.id() != selection.Window && !(key.Kind == selection.Window && key.Scope == "all") {
				continue
			}
			g := groups[key]
			if g == nil {
				g = &viewGroup{Key: key, Total: quotaTotal{Contributors: []string{}, Missing: []string{}, Normalization: "Soma de percentuais de contas com janelas compatíveis; não é capacidade compartilhada."}, Accounts: []accountAnalysis{}}
				groups[key] = g
			}
			h := historyFor(data, a.ID, key)
			result := analyze(h, analysisOptions{Now: now, Cadence: cadence(a.Provider), Period: selection.Period, Lookback: selection.Lookback, Horizon: selection.Horizon, Recurrence: selection.Recurrence})
			freshness := "missing"
			for _, w := range a.Windows {
				if w.Key == key {
					freshness = w.Freshness
					g.Label = w.Label
					if w.Remaining != nil {
						v := *w.Remaining
						if g.Total.Remaining != nil {
							v += *g.Total.Remaining
						}
						g.Total.Remaining = &v
						g.Total.Capacity += w.Capacity
						g.Total.Contributors = append(g.Total.Contributors, a.ID)
					} else {
						g.Total.Missing = append(g.Total.Missing, a.ID)
					}
				}
			}
			if !a.Active || freshness != "fresh" {
				result.Forecast = []forecastPoint{}
				result.ExhaustsAt = nil
				result.Insufficient = "Conta fora de sessão ou leitura desatualizada. Projeção suspensa."
			}
			g.Accounts = append(g.Accounts, accountAnalysis{WindowID: key.id(), AccountID: a.ID, Label: a.Identity.Label, Freshness: freshness, analysis: result})
		}
	}
	for _, g := range groups {
		for _, a := range snap.Accounts {
			if string(a.Provider) != selection.Provider || !a.Active || (selection.Account != "" && selection.Account != a.ID) {
				continue
			}
			found := false
			for _, id := range append(append([]string{}, g.Total.Contributors...), g.Total.Missing...) {
				if a.ID == id {
					found = true
				}
			}
			if !found {
				g.Total.Missing = append(g.Total.Missing, a.ID)
			}
		}
		out.Groups = append(out.Groups, *g)
	}
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Key.id() < out.Groups[j].Key.id() })
	if len(out.Groups) == 0 {
		out.SelectionRequired = "Nenhuma leitura desta janela. Aguarde a coleta ou selecione outra janela."
	}
	return out, nil
}
