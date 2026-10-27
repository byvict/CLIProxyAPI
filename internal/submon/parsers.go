package submon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errShape = errors.New("unrecognized native data")
var errInvalidPercent = errors.New("invalid quota percentage")

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
			return c, errCredentialAPIKey
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
		return c, errCredentialMissing
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
		Email string          `json:"user_email"`
		Usage json.RawMessage `json:"subs_usage"`
	}
	if json.Unmarshal(b, &d) != nil {
		return missing{Status: invalidResponse}
	}
	email := normalizedEmail(d.Email)
	if email == "" {
		return missing{Status: identityUnverified}
	}
	i := identity{Provider: muse, Basis: providerEmail, Subject: email, Email: email}
	if len(d.Usage) == 0 || string(d.Usage) == "null" {
		return missing{Identity: i, Status: unavailable, Code: "dormant"}
	}
	w, err := parseWindows(muse, b)
	if err != nil {
		return missing{Identity: i, Status: invalidResponse}
	}
	return reading{Identity: i, Windows: accountWindowBasis(w, i)}
}
func remaining(used *float64) (*float64, error) {
	if used == nil {
		return nil, nil
	}
	if math.IsNaN(*used) || math.IsInf(*used, 0) || *used < 0 || *used > 100 {
		return nil, errInvalidPercent
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
		if seconds < 0 || seconds > int64((1<<63-1)/int64(time.Second)) || (seconds == 0 && kind != "monthly") || scope == "" {
			return errShape
		}
		if reset != nil && (reset.Year() < 1 || reset.Year() > 9999) {
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
		type limitSet struct {
			Primary   *limit `json:"primary_window"`
			Secondary *limit `json:"secondary_window"`
		}
		var d struct {
			Plan                string          `json:"plan_type"`
			Limits              *limitSet       `json:"rate_limit"`
			Additional          json.RawMessage `json:"additional_rate_limits"`
			CodeReview          json.RawMessage `json:"code_review_rate_limit"`
			AlternateCodeReview json.RawMessage `json:"code_review_rate_limits"`
		}
		if json.Unmarshal(b, &d) != nil || d.Limits == nil {
			return nil, errShape
		}
		basis := d.Plan
		if basis == "" {
			basis = "unknown-plan"
		}
		addSet := func(set *limitSet, scope, suffix string) error {
			for _, l := range []*limit{set.Primary, set.Secondary} {
				if l == nil {
					continue
				}
				kind := "session"
				if l.Seconds == 604800 {
					kind = "weekly"
				} else if l.Seconds >= 86400 {
					kind = "period"
				}
				if err := add(kind, scope, span(l.Seconds)+suffix, basis, l.Seconds, l.Used, unixTime(l.Reset)); err != nil {
					return err
				}
			}
			return nil
		}
		if d.Limits.Primary == nil && d.Limits.Secondary == nil {
			return nil, errShape
		}
		if err := addSet(d.Limits, "all", ""); err != nil {
			return nil, err
		}
		unknownScopes := map[string]bool{}
		unknown := func(scope string) {
			unknownScopes[scope] = true
			kept := out[:0]
			for _, w := range out {
				if w.Key.Scope != scope || scope == "all" {
					kept = append(kept, w)
				}
			}
			out = kept
			key := windowKey{Kind: "unsupported", Scope: scope, Unit: "percent", CapacityBasis: basis}
			for _, w := range out {
				if w.Key == key {
					return
				}
			}
			out = append(out, windowReading{Key: key, Label: "Limite adicional desconhecido · " + scope, Capacity: 100})
		}
		optional := func(raw json.RawMessage, scope, suffix string) error {
			if len(raw) == 0 || string(raw) == "null" {
				return nil
			}
			var set limitSet
			var nested struct {
				Limits json.RawMessage `json:"rate_limit"`
			}
			if json.Unmarshal(raw, &nested) != nil {
				unknown(scope)
				return nil
			}
			if len(nested.Limits) > 0 && string(nested.Limits) != "null" {
				var object map[string]json.RawMessage
				if json.Unmarshal(nested.Limits, &object) != nil {
					unknown(scope)
					return nil
				}
				if len(object) > 0 {
					raw = nested.Limits
				}
			}
			if json.Unmarshal(raw, &set) != nil {
				unknown(scope)
				return nil
			}
			if set.Primary == nil && set.Secondary == nil {
				var flags struct {
					Allowed *bool `json:"allowed"`
					Reached *bool `json:"limit_reached"`
				}
				if json.Unmarshal(raw, &flags) != nil || (flags.Allowed != nil && !*flags.Allowed) || (flags.Reached != nil && *flags.Reached) {
					unknown(scope)
					return nil
				}
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(raw, &fields)
				for key := range fields {
					if key != "primary_window" && key != "secondary_window" && key != "allowed" && key != "limit_reached" && key != "limit_name" && key != "metered_feature" && key != "rate_limit" {
						unknown(scope)
						break
					}
				}
				return nil
			}
			for _, l := range []*limit{set.Primary, set.Secondary} {
				if l != nil {
					if _, err := remaining(l.Used); err != nil {
						return err
					}
				}
			}
			if scope == "all" {
				unknown("additional:all")
				return nil
			}
			if unknownScopes[scope] {
				return nil
			}
			start := len(out)
			if err := addSet(&set, scope, suffix); err != nil {
				if errors.Is(err, errInvalidPercent) {
					return err
				}
				out = out[:start]
				unknown(scope)
			}
			return nil
		}
		if len(d.Additional) > 0 && string(d.Additional) != "null" {
			var entries []json.RawMessage
			if json.Unmarshal(d.Additional, &entries) != nil {
				unknown("additional-unknown")
			} else {
				for _, raw := range entries {
					var entry struct {
						Name string `json:"limit_name"`
					}
					if json.Unmarshal(raw, &entry) != nil || strings.TrimSpace(entry.Name) == "" {
						unknown("additional-unknown")
						continue
					}
					name := strings.TrimSpace(entry.Name)
					if err := optional(raw, name, " "+name); err != nil {
						return nil, err
					}
				}
			}
		}
		codeReview := d.CodeReview
		if len(codeReview) == 0 || string(codeReview) == "null" {
			codeReview = d.AlternateCodeReview
		}
		if err := optional(codeReview, "code-review", " code review"); err != nil {
			return nil, err
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
			scoped := kind == "weekly_scoped"
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
				return nil, errShape
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
			if scoped && scope == "all" {
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

func accountWindowBasis(windows []windowReading, i identity) []windowReading {
	for j := range windows {
		switch windows[j].Key.CapacityBasis {
		case "unknown-plan", "native-subscription", "native-credits":
			windows[j].Key.CapacityBasis = "account-percent:" + i.accountID()
		}
	}
	return windows
}

func parseCodexDetails(body []byte) accountDetails {
	var raw struct {
		Credits struct {
			Available json.RawMessage `json:"available_count"`
		} `json:"rate_limit_reset_credits"`
		Models map[string]json.RawMessage `json:"model_usage"`
	}
	if json.Unmarshal(body, &raw) != nil {
		return accountDetails{}
	}
	details := accountDetails{}
	var credits *int
	if json.Unmarshal(raw.Credits.Available, &credits) == nil && credits != nil && *credits >= 0 {
		details.ResetCredits = credits
	}
	for name, data := range raw.Models {
		value := modelAvailability{Model: name}
		var parsed struct {
			Available   *bool           `json:"available"`
			AvailableAt json.RawMessage `json:"available_at"`
		}
		if json.Unmarshal(data, &parsed) == nil {
			value.Available = parsed.Available
			value.AvailableAt = parseAvailabilityTime(parsed.AvailableAt)
		}
		details.Models = append(details.Models, value)
	}
	sort.Slice(details.Models, func(i, j int) bool { return details.Models[i].Model < details.Models[j].Model })
	return details
}

func parseAvailabilityTime(raw json.RawMessage) *time.Time {
	text := strings.TrimSpace(string(raw))
	var decoded string
	if json.Unmarshal(raw, &decoded) == nil {
		if parsed, err := time.Parse(time.RFC3339Nano, decoded); err == nil && parsed.Year() >= 1 && parsed.Year() <= 9999 {
			return &parsed
		}
		text = decoded
	}
	seconds, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds >= float64(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC).Unix()) {
		return nil
	}
	whole, fraction := math.Modf(seconds)
	stamp := time.Unix(int64(whole), int64(fraction*1e9)).UTC()
	return &stamp
}
