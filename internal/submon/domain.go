package submon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type provider string

const (
	codex  provider = "codex"
	claude provider = "claude"
	grok   provider = "grok"
	muse   provider = "muse"
)

var providers = []provider{codex, claude, grok, muse}

type identityBasis string

const (
	providerAccount identityBasis = "provider_account"
	providerEmail   identityBasis = "provider_email"
)

type identity struct {
	Provider provider
	Basis    identityBasis
	Subject  string
	Email    string
}

func (i identity) accountID() string {
	sum := sha256.Sum256([]byte(string(i.Provider) + "\x00" + string(i.Basis) + "\x00" + i.Subject))
	return hex.EncodeToString(sum[:16])
}

type status string

const (
	ok                 status = "ok"
	unavailable        status = "unavailable"
	authFailure        status = "auth_failure"
	rateLimited        status = "rate_limited"
	identityUnverified status = "identity_unverified"
	invalidResponse    status = "invalid_response"
	transportFailure   status = "transport_error"
)

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

type probeOutcome interface{ probeOutcome() }
type reading struct {
	Identity identity
	Windows  []windowReading
}

func (reading) probeOutcome() {}

type missing struct {
	Identity identity
	Status   status
	Code     string
	RetryAt  time.Time
}

func (missing) probeOutcome() {}

type probe struct {
	ID          string
	SourceID    string
	StartedAt   time.Time
	At          time.Time
	NotBefore   time.Time
	Outcome     probeOutcome
	SideEffects []string
}

type source struct {
	ID       string
	Provider provider
	Cadence  time.Duration
	Probe    func(ctx probeContext) probe
}

type probeContext struct {
	Context context.Context
	Now     func() time.Time
}

type sample struct {
	At     time.Time     `json:"at"`
	Window windowReading `json:"window"`
}
type limitingWindow struct {
	windowKey
	Label     string     `json:"label"`
	ReturnsAt *time.Time `json:"returnsAt"`
}
type availability struct {
	State    string           `json:"state"`
	Limiting []limitingWindow `json:"limiting"`
}

func availabilityOf(windows []windowReading) availability {
	result := availability{State: "available", Limiting: []limitingWindow{}}
	known := false
	unknown := false
	for _, w := range windows {
		if w.Remaining == nil {
			unknown = true
			continue
		}
		known = true
		if *w.Remaining == 0 {
			result.Limiting = append(result.Limiting, limitingWindow{windowKey: w.Key, Label: w.Label, ReturnsAt: w.ResetsAt})
			if w.Key.Scope == "all" {
				result.State = "unavailable"
			}
		}
	}
	if result.State != "unavailable" {
		if !known || unknown {
			result.State = "unknown"
		} else if len(result.Limiting) > 0 {
			result.State = "limited"
		}
	}
	return result
}
func cadence(p provider) time.Duration {
	if p == muse {
		return 30 * time.Minute
	}
	return 5 * time.Minute
}
func statusMessage(s status, code string) string {
	switch s {
	case ok:
		return "Leitura válida."
	case authFailure:
		return "Login expirado ou recusado. Renove pela CLI nativa."
	case rateLimited:
		return "Provedor limitou as consultas. Aguardando nova tentativa."
	case identityUnverified:
		return "Identidade não verificada. A cota não foi vinculada a uma conta."
	case transportFailure:
		return "Falha de transporte ao consultar o provedor."
	case invalidResponse:
		return "Resposta ou credencial local em formato não reconhecido."
	default:
		if code == "dormant" {
			return "Cota ausente no Muse. Nenhuma janela foi despertada."
		}
		if code == "api_key" {
			return "Credencial de API não representa uma assinatura."
		}
		return "Credencial ou cota indisponível na fonte nativa."
	}
}
