package submon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"strings"
	"time"
)

var errShape = errors.New("unrecognized native data")

type credential struct {
	Access   string
	Account  string
	Identity identity
}

func tokenClaims(token string) (string, string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var c struct {
		Issuer  string `json:"iss"`
		Subject string `json:"sub"`
	}
	if json.Unmarshal(b, &c) != nil {
		return "", ""
	}
	return c.Issuer, c.Subject
}
func parseCredential(p provider, b []byte) (credential, error) {
	var c credential
	switch p {
	case codex:
		var d struct {
			Tokens struct {
				Access  string `json:"access_token"`
				Account string `json:"account_id"`
			} `json:"tokens"`
		}
		if json.Unmarshal(b, &d) != nil {
			return c, errShape
		}
		c.Access = d.Tokens.Access
		c.Account = d.Tokens.Account
	case grok:
		var d map[string]struct {
			Access string `json:"key"`
			UserID string `json:"user_id"`
			Mode   string `json:"auth_mode"`
		}
		if json.Unmarshal(b, &d) != nil {
			return c, errShape
		}
		e, found := d["https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828"]
		if !found {
			e = d["https://accounts.x.ai/sign-in"]
		}
		if e.Mode == "api_key" {
			return c, errors.New("api_key")
		}
		c.Access = e.Access
		c.Account = e.UserID
	case claude:
		var d struct {
			OAuth struct {
				Access string `json:"accessToken"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(b, &d) != nil {
			return c, errShape
		}
		c.Access = d.OAuth.Access
	case muse:
		var d struct {
			Access    string `json:"access_token"`
			Providers struct {
				Meta struct {
					Access    string `json:"access_token"`
					Mechanism string `json:"mechanism"`
				} `json:"meta"`
			} `json:"providers"`
		}
		if json.Unmarshal(b, &d) != nil {
			return c, errShape
		}
		c.Access = d.Access
		if c.Access == "" && d.Providers.Meta.Mechanism == "oauth" {
			c.Access = d.Providers.Meta.Access
		}
	}
	if strings.TrimSpace(c.Access) == "" {
		return c, errors.New("credential_missing")
	}
	if p == codex || p == grok {
		issuer, sub := tokenClaims(c.Access)
		if issuer != "" && sub != "" && c.Account != "" {
			parts, _ := json.Marshal([]string{issuer, c.Account, sub})
			c.Identity = identity{Provider: p, Basis: providerAccount, Subject: string(parts)}
		}
	}
	return c, nil
}
func normalizedEmail(raw string) string {
	email := strings.ToLower(strings.TrimSpace(raw))
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email {
		return ""
	}
	return email
}
func parseProfile(b []byte) identity {
	var d struct {
		Account struct {
			UUID  string `json:"uuid"`
			Email string `json:"email"`
		} `json:"account"`
	}
	if json.Unmarshal(b, &d) != nil || strings.TrimSpace(d.Account.UUID) == "" {
		return identity{}
	}
	return identity{Provider: claude, Basis: providerAccount, Subject: d.Account.UUID, Email: normalizedEmail(d.Account.Email)}
}
func parseMuse(b []byte) probeOutcome {
	var d struct {
		Email string `json:"user_email"`
	}
	if json.Unmarshal(b, &d) != nil {
		return missing{Status: invalidResponse}
	}
	email := normalizedEmail(d.Email)
	if email == "" {
		return missing{Status: identityUnverified}
	}
	w, err := parseWindows(muse, b)
	if err != nil {
		return missing{Status: unavailable, Code: "dormant"}
	}
	return reading{Identity: identity{Provider: muse, Basis: providerEmail, Subject: email, Email: email}, Windows: w}
}
func remaining(used *float64) (*float64, error) {
	if used == nil {
		return nil, nil
	}
	if math.IsNaN(*used) || math.IsInf(*used, 0) || *used < 0 || *used > 100 {
		return nil, errShape
	}
	r := 100 - *used
	return &r, nil
}
func unixTime(n *int64) *time.Time {
	if n == nil || *n <= 0 {
		return nil
	}
	t := time.Unix(*n, 0).UTC()
	return &t
}
func span(seconds int64) string {
	for _, d := range []struct {
		n int64
		s string
	}{{86400, "d"}, {3600, "h"}, {60, "min"}} {
		if seconds > 0 && seconds%d.n == 0 {
			return fmt.Sprintf("%d%s", seconds/d.n, d.s)
		}
	}
	return "janela"
}
func parseWindows(p provider, b []byte) ([]windowReading, error) {
	out := []windowReading{}
	add := func(kind, scope, label, basis string, seconds int64, used *float64, reset *time.Time) error {
		r, err := remaining(used)
		if err != nil {
			return err
		}
		if seconds < 0 || (seconds == 0 && kind != "monthly") || scope == "" {
			return errShape
		}
		w := windowReading{Key: windowKey{Kind: kind, Scope: scope, PeriodSeconds: seconds, Unit: "percent", CapacityBasis: basis}, Label: label, Capacity: 100, Remaining: r, ResetsAt: reset}
		for _, prior := range out {
			if prior.Key == w.Key {
				return errShape
			}
		}
		out = append(out, w)
		return nil
	}
	switch p {
	case codex:
		type limit struct {
			Used    *float64 `json:"used_percent"`
			Reset   *int64   `json:"reset_at"`
			Seconds int64    `json:"limit_window_seconds"`
		}
		var d struct {
			Plan   string `json:"plan_type"`
			Limits *struct {
				Primary   *limit `json:"primary_window"`
				Secondary *limit `json:"secondary_window"`
			} `json:"rate_limit"`
		}
		if json.Unmarshal(b, &d) != nil || d.Limits == nil {
			return nil, errShape
		}
		basis := d.Plan
		if basis == "" {
			basis = "unknown-plan"
		}
		for _, l := range []*limit{d.Limits.Primary, d.Limits.Secondary} {
			if l == nil {
				continue
			}
			kind := "session"
			if l.Seconds == 604800 {
				kind = "weekly"
			} else if l.Seconds >= 86400 {
				kind = "period"
			}
			if err := add(kind, "all", span(l.Seconds), basis, l.Seconds, l.Used, unixTime(l.Reset)); err != nil {
				return nil, err
			}
		}
	case claude:
		var d struct {
			Limits []struct {
				Kind    string     `json:"kind"`
				Group   string     `json:"group"`
				Percent *float64   `json:"percent"`
				Reset   *time.Time `json:"resets_at"`
				Scope   *struct {
					Model struct {
						ID   string `json:"id"`
						Name string `json:"display_name"`
					} `json:"model"`
				} `json:"scope"`
			} `json:"limits"`
		}
		if json.Unmarshal(b, &d) != nil || d.Limits == nil {
			return nil, errShape
		}
		for _, l := range d.Limits {
			kind := l.Kind
			if kind == "" {
				kind = l.Group
			}
			seconds := int64(604800)
			label := "semanal"
			scope := "all"
			switch kind {
			case "session":
				seconds = 18000
				label = "sessão"
			case "weekly_all", "weekly_scoped":
				kind = "weekly"
			default:
				continue
			}
			if l.Scope != nil {
				scope = l.Scope.Model.ID
				if scope == "" && l.Scope.Model.Name != "" {
					scope = "provider-label:" + l.Scope.Model.Name
				}
				if scope == "" {
					return nil, errShape
				}
				name := l.Scope.Model.Name
				if name == "" {
					name = scope
				}
				label += " " + name
			}
			if l.Kind == "weekly_scoped" && scope == "all" {
				return nil, errShape
			}
			if err := add(kind, scope, label, "native-subscription", seconds, l.Percent, l.Reset); err != nil {
				return nil, err
			}
		}
	case grok:
		var d struct {
			Config *struct {
				Used   *float64 `json:"creditUsagePercent"`
				Period struct {
					Type string     `json:"type"`
					End  *time.Time `json:"end"`
				} `json:"currentPeriod"`
			} `json:"config"`
		}
		if json.Unmarshal(b, &d) != nil || d.Config == nil {
			return nil, errShape
		}
		l := d.Config
		kind, label, seconds := "weekly", "semanal", int64(604800)
		if l.Period.Type == "USAGE_PERIOD_TYPE_MONTHLY" {
			kind, label, seconds = "monthly", "mensal", 0
		}
		if l.Period.Type != "USAGE_PERIOD_TYPE_WEEKLY" && l.Period.Type != "USAGE_PERIOD_TYPE_MONTHLY" {
			return nil, errShape
		}
		if err := add(kind, "all", label, "native-credits", seconds, l.Used, l.Period.End); err != nil {
			return nil, err
		}
	case muse:
		type limit struct {
			Used    *float64 `json:"used_percent"`
			Reset   *int64   `json:"resets_at"`
			Minutes int64    `json:"window_duration_mins"`
		}
		var d struct {
			Usage *struct {
				Short  *limit `json:"window"`
				Weekly *limit `json:"weekly"`
			} `json:"subs_usage"`
		}
		if json.Unmarshal(b, &d) != nil || d.Usage == nil || d.Usage.Weekly == nil {
			return nil, errShape
		}
		if l := d.Usage.Short; l != nil {
			if err := add("session", "all", span(l.Minutes*60), "native-subscription", l.Minutes*60, l.Used, unixTime(l.Reset)); err != nil {
				return nil, err
			}
		}
		l := d.Usage.Weekly
		if err := add("weekly", "all", "semanal", "native-subscription", 604800, l.Used, unixTime(l.Reset)); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, errShape
	}
	return out, nil
}
