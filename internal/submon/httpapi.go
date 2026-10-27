package submon

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui/*
var uiFiles embed.FS

type api struct {
	store     *store
	collector *collector
	keyHash   [32]byte
	now       func() time.Time
	hosts     map[string]bool
	assets    http.Handler
	mu        sync.Mutex
	sessions  map[[32]byte]time.Time
}

func newAPI(s *store, c *collector, key string, now func() time.Time, listen string) *api {
	_, port, _ := net.SplitHostPort(listen)
	hosts := map[string]bool{}
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		hosts[net.JoinHostPort(host, port)] = true
	}
	sub, _ := fs.Sub(uiFiles, "ui")
	return &api{store: s, collector: c, keyHash: sha256.Sum256([]byte(key)), now: now, hosts: hosts, assets: http.FileServer(http.FS(sub)), sessions: map[[32]byte]time.Time{}}
}
func (a *api) validKey(key string) bool {
	hash := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(hash[:], a.keyHash[:]) == 1
}
func (a *api) authenticated(r *http.Request) (bool, bool) {
	if raw := r.Header.Get("Authorization"); raw != "" {
		key, found := strings.CutPrefix(raw, "Bearer ")
		return found && a.validKey(key), true
	}
	c, err := r.Cookie("submon_session")
	if err != nil {
		return false, false
	}
	hash := sha256.Sum256([]byte(c.Value))
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, found := a.sessions[hash]
	return found && a.now().Before(expires), false
}
func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if !a.hosts[strings.ToLower(r.Host)] {
		apiError(w, 403, "Host não permitido.")
		return
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(remote).IsLoopback() {
		apiError(w, 403, "Acesso restrito ao loopback.")
		return
	}
	origin := r.Header.Get("Origin")
	expected := "http://" + r.Host
	if origin != "" && origin != expected {
		apiError(w, 403, "Origem não permitida.")
		return
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/app.js" || r.URL.Path == "/style.css") {
		a.assets.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/session" {
		if r.Method != "POST" {
			methodError(w)
			return
		}
		if origin != expected {
			apiError(w, 403, "Origem obrigatória para a sessão.")
			return
		}
		a.login(w, r)
		return
	}
	authenticated, bearer := a.authenticated(r)
	if !authenticated {
		apiError(w, 401, "Autenticação necessária.")
		return
	}
	if r.Method != "GET" && (!bearer || origin != "") && origin != expected {
		apiError(w, 403, "Origem obrigatória para esta ação.")
		return
	}
	switch {
	case r.URL.Path == "/api/v1/snapshot":
		if r.Method != "GET" {
			methodError(w)
			return
		}
		a.snapshot(w, r)
	case r.URL.Path == "/api/v1/view":
		if r.Method != "GET" {
			methodError(w)
			return
		}
		a.view(w, r)
	case r.URL.Path == "/api/v1/refresh":
		if r.Method != "POST" {
			methodError(w)
			return
		}
		a.refresh(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/accounts/") && strings.HasSuffix(r.URL.Path, "/email"):
		if r.Method != "POST" {
			methodError(w)
			return
		}
		a.email(w, r)
	default:
		apiError(w, 404, "Recurso não encontrado.")
	}
}
func apiJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, code int, message string) {
	apiJSON(w, code, struct {
		Message string `json:"message"`
	}{message})
}
func methodError(w http.ResponseWriter) { apiError(w, 405, "Método não permitido.") }
func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		apiError(w, 415, "Use application/json.")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		apiError(w, 400, "Corpo inválido.")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		apiError(w, 400, "Corpo inválido.")
		return false
	}
	return true
}
func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !a.validKey(body.Key) {
		apiError(w, 401, "Chave inválida.")
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		apiError(w, 500, "Não foi possível criar a sessão.")
		return
	}
	value := hex.EncodeToString(token)
	hash := sha256.Sum256([]byte(value))
	now := a.now()
	a.mu.Lock()
	for h, expiry := range a.sessions {
		if !now.Before(expiry) {
			delete(a.sessions, h)
		}
	}
	if len(a.sessions) >= 32 {
		a.mu.Unlock()
		apiError(w, 429, "Limite de sessões atingido.")
		return
	}
	a.sessions[hash] = now.Add(12 * time.Hour)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "submon_session", Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600})
	apiJSON(w, 200, struct {
		Authenticated bool `json:"authenticated"`
	}{true})
}
func (a *api) snapshot(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format != "" && format != "t3-usage" {
		apiError(w, 400, "Formato inválido.")
		return
	}
	now := a.now()
	data, err := a.store.loadSince(now.Add(-24 * time.Hour))
	if err != nil {
		apiError(w, 500, "Não foi possível ler o histórico.")
		return
	}
	s := makeSnapshot(data, a.collector.states(), now)
	if format == "t3-usage" {
		apiJSON(w, 200, usageSnapshot(s, now))
		return
	}
	apiJSON(w, 200, s)
}
func parseDuration(value string, allowed map[string]time.Duration, defaultValue time.Duration) (time.Duration, bool) {
	if value == "" {
		return defaultValue, true
	}
	d, found := allowed[value]
	return d, found
}
func (a *api) view(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	period, pOK := parseDuration(q.Get("period"), map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}, 24*time.Hour)
	lookback, lOK := parseDuration(q.Get("lookback"), map[string]time.Duration{"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour}, time.Hour)
	horizon, hOK := parseDuration(q.Get("horizon"), map[string]time.Duration{"3d": 3 * 24 * time.Hour, "7d": 7 * 24 * time.Hour, "14d": 14 * 24 * time.Hour}, 7*24*time.Hour)
	recurrence := true
	var err error
	if q.Has("recurrence") {
		recurrence, err = strconv.ParseBool(q.Get("recurrence"))
	}
	if !pOK || !lOK || !hOK || err != nil {
		apiError(w, 400, "Período inválido.")
		return
	}
	now := a.now()
	data, err := a.store.loadSince(now.Add(-period))
	if err != nil {
		apiError(w, 500, "Não foi possível ler o histórico.")
		return
	}
	v, err := makeView(data, a.collector.states(), now, viewSelection{Provider: q.Get("provider"), Account: q.Get("account"), Window: q.Get("window"), Period: period, Lookback: lookback, Horizon: horizon, Recurrence: recurrence})
	if err != nil {
		apiError(w, 400, "Seleção inválida.")
		return
	}
	apiJSON(w, 200, v)
}
func (a *api) refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source  string `json:"source"`
		Account string `json:"account"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Source != "" {
		if _, found := a.collector.workers[body.Source]; !found {
			apiError(w, 400, "Fonte desconhecida.")
			return
		}
	}
	if body.Account != "" {
		sources, err := a.store.currentSources(body.Account)
		if err != nil {
			apiError(w, 500, "Não foi possível ler a conta.")
			return
		}
		found := false
		for _, source := range sources {
			if _, exists := a.collector.workers[source]; exists {
				a.collector.refresh(source)
				found = true
			}
		}
		if !found {
			apiError(w, 409, "Conta fora da sessão nativa atual.")
			return
		}
		apiJSON(w, 202, a.collector.states())
		return
	}
	apiJSON(w, 202, a.collector.refresh(body.Source))
}
func (a *api) email(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/"), "/email")
	if len(id) != 32 || strings.Contains(id, "/") {
		apiError(w, 404, "Conta não encontrada.")
		return
	}
	email, err := a.store.emailFor(id)
	if err != nil {
		apiError(w, 500, "Não foi possível ler a conta.")
		return
	}
	if email == "" {
		apiError(w, 404, "E-mail não informado pelo provedor.")
		return
	}
	apiJSON(w, 200, struct {
		Email string `json:"email"`
	}{email})
}
