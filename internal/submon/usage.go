package submon

import "time"

type usageWindow struct {
	Label       string     `json:"label"`
	UsedPercent *float64   `json:"usedPercent"`
	ResetsAt    *time.Time `json:"resetsAt"`
}
type usageProvider struct {
	Provider    provider      `json:"provider"`
	Status      string        `json:"status"`
	CheckedAt   time.Time     `json:"checkedAt"`
	Windows     []usageWindow `json:"windows"`
	Message     string        `json:"message,omitempty"`
	SideEffects []string      `json:"sideEffects,omitempty"`
}
type usageReport struct {
	GeneratedAt    time.Time       `json:"generatedAt"`
	CheckedAtBasis string          `json:"checkedAtBasis"`
	CheckedAt      time.Time       `json:"checkedAt"`
	Timezone       string          `json:"timezone"`
	Providers      []usageProvider `json:"providers"`
}

func usageSnapshot(s snapshot, now time.Time) usageReport {
	out := usageReport{GeneratedAt: now.In(saoPaulo), CheckedAt: time.Unix(0, 0).In(saoPaulo), CheckedAtBasis: "none", Timezone: "America/Sao_Paulo", Providers: []usageProvider{}}
	var oldest *time.Time
	var oldestAttempt *time.Time
	for _, src := range s.Sources {
		p := usageProvider{Provider: src.Provider, Status: "unavailable", CheckedAt: time.Unix(0, 0).In(saoPaulo), Windows: []usageWindow{}, Message: src.Message, SideEffects: src.SideEffects}
		if src.Status == authFailure || src.Status == rateLimited || src.Status == transportFailure || src.Status == invalidResponse {
			p.Status = "error"
		}
		if src.LastAttemptAt != nil {
			p.CheckedAt = src.LastAttemptAt.In(saoPaulo)
			if oldestAttempt == nil || p.CheckedAt.Before(*oldestAttempt) {
				at := p.CheckedAt
				oldestAttempt = &at
			}
		}
		for _, a := range s.Accounts {
			if !a.Active || a.ID != src.CurrentAccountID {
				continue
			}
			if a.ObservedAt != nil {
				p.CheckedAt = a.ObservedAt.In(saoPaulo)
			}
			if a.Status == string(ok) && a.Freshness == "fresh" && src.Status == ok {
				p.Status = "ok"
				p.Message = ""
			} else if src.Status == authFailure || src.Status == rateLimited || src.Status == transportFailure || src.Status == invalidResponse {
				p.Status = "error"
			} else {
				p.Status = "unavailable"
			}
			if a.Freshness != "fresh" && p.Status != "error" {
				p.Message = "Leitura ausente ou desatualizada. Consulte o snapshot completo para idade e estado."
			}
			for _, w := range a.Windows {
				var used *float64
				if w.Remaining != nil {
					v := 100 - *w.Remaining
					used = &v
				}
				var reset *time.Time
				if w.ResetsAt != nil {
					v := w.ResetsAt.In(saoPaulo)
					reset = &v
				}
				p.Windows = append(p.Windows, usageWindow{Label: w.Label, UsedPercent: used, ResetsAt: reset})
			}
		}
		if p.Status == "ok" && (oldest == nil || p.CheckedAt.Before(*oldest)) {
			at := p.CheckedAt
			oldest = &at
		}
		out.Providers = append(out.Providers, p)
	}
	if oldest != nil {
		out.CheckedAt = *oldest
		out.CheckedAtBasis = "observation"
	} else if oldestAttempt != nil {
		out.CheckedAt = *oldestAttempt
		out.CheckedAtBasis = "attempt"
	}
	return out
}
