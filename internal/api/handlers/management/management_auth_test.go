package management

import (
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func authHash(t testing.TB, key string, cost int) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(key), cost)
	if err != nil {
		t.Fatal(err)
	}
	return string(hash)
}

func authHandler(hash string) *Handler {
	h := &Handler{cfg: &config.Config{RemoteManagement: config.RemoteManagement{AllowRemote: true, SecretKey: hash}}, failedAttempts: make(map[string]*attemptInfo)}
	rand.Read(h.managementMemoKey[:])
	return h
}

func assertManagementAuth(t *testing.T, h *Handler, ip string, local bool, key string, status int) {
	t.Helper()
	allowed, got, message := h.AuthenticateManagementKey(ip, local, key)
	if got != status || allowed != (status == 0) {
		t.Fatalf("auth status=%d allowed=%v message=%q, want %d", got, allowed, message, status)
	}
}

func TestManagementAuthMemoPolicy(t *testing.T) {
	hash := authHash(t, "correct", bcrypt.MinCost)
	h := authHandler(hash)
	assertManagementAuth(t, h, "remote", false, "correct", 0)
	initial := h.verifiedManagementKey
	if !initial.valid {
		t.Fatal("successful configured key must populate memo")
	}
	assertManagementAuth(t, h, "remote", false, "wrong", 401)
	assertManagementAuth(t, h, "remote", false, "correct", 0)
	if h.failedAttempts["remote"].count != 0 {
		t.Fatal("warm success did not reset failures")
	}
	for range 5 {
		assertManagementAuth(t, h, "remote", false, "wrong", 401)
	}
	if h.verifiedManagementKey != initial {
		t.Fatal("wrong keys changed the successful memo")
	}
	assertManagementAuth(t, h, "remote", false, "correct", 403)
	h.SetLocalPassword("local")
	h.envSecret = "env"
	assertManagementAuth(t, h, "remote", true, "local", 403)
	assertManagementAuth(t, h, "remote", false, "env", 403)
	h.failedAttempts["remote"].blockedUntil = time.Now().Add(-time.Minute)
	assertManagementAuth(t, h, "remote", false, "correct", 0)
	cfg := h.cfg.CloneForRuntime()
	cfg.RemoteManagement.AllowRemote = false
	h.SetConfig(cfg)
	assertManagementAuth(t, h, "remote", false, "correct", 403)
	assertManagementAuth(t, h, "remote", true, "correct", 0)
	assertManagementAuth(t, h, "other", true, "local", 0)
	assertManagementAuth(t, h, "other", false, "local", 403)
	h.allowRemoteOverride = true
	assertManagementAuth(t, h, "remote", false, "correct", 0)
	assertManagementAuth(t, h, "remote", false, "env", 0)
	cfg.RemoteManagement.SecretKey = ""
	h.SetConfig(cfg)
	assertManagementAuth(t, h, "other", true, "correct", 401)
	h.envSecret = ""
	assertManagementAuth(t, h, "other", true, "local", 403)
	h.SetConfig(nil)
	assertManagementAuth(t, h, "other", true, "local", 403)
	var nilHandler *Handler
	assertManagementAuth(t, nilHandler, "other", true, "local", 403)
}

func TestManagementAuthMemoRotationWriters(t *testing.T) {
	for _, writer := range []string{"SetConfig", "ConfigV8", "PutConfigYAML"} {
		t.Run(writer, func(t *testing.T) {
			hashA := authHash(t, "alpha", bcrypt.MinCost)
			hashB := authHash(t, "bravo", bcrypt.MinCost)
			path := filepath.Join(t.TempDir(), "config.yaml")
			raw := func(hash string) string {
				return "config-version: 8\nserver: {port: 8317}\nmanagement:\n  allow-remote: true\n  secret-key: '" + hash + "'\n"
			}
			if err := os.WriteFile(path, []byte(raw(hashA)), 0600); err != nil {
				t.Fatal(err)
			}
			h := authHandler(hashA)
			h.configFilePath = path
			write := func(hash string) {
				if writer == "SetConfig" {
					cfg := h.cfg.CloneForRuntime()
					cfg.RemoteManagement.SecretKey = hash
					h.SetConfig(cfg)
					return
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPut, "/v8/management/config.yaml", strings.NewReader(raw(hash)))
				if writer == "ConfigV8" {
					r := gin.New()
					r.PUT("/v8/management/config.yaml", h.ConfigV8)
					r.ServeHTTP(recorder, c.Request)
				} else {
					h.PutConfigYAML(c)
				}
				if recorder.Code != 200 {
					t.Fatalf("write status=%d body=%s", recorder.Code, recorder.Body.String())
				}
			}
			assertManagementAuth(t, h, "a", true, "alpha", 0)
			write(hashB)
			assertManagementAuth(t, h, "a", true, "alpha", 401)
			assertManagementAuth(t, h, "a", true, "bravo", 0)
			previous := h.verifiedManagementKey
			write(authHash(t, "bravo", bcrypt.MinCost))
			assertManagementAuth(t, h, "a", true, "bravo", 0)
			if previous == h.verifiedManagementKey {
				t.Fatal("new hash must bind a new memo")
			}
			write("")
			assertManagementAuth(t, h, "a", true, "bravo", 403)
		})
	}
}

func TestManagementAuthMemoPrivateAndSuccessOnly(t *testing.T) {
	hash := authHash(t, "correct", bcrypt.MinCost)
	a, b := authHandler(hash), authHandler(hash)
	assertManagementAuth(t, a, "a", true, "wrong", 401)
	assertManagementAuth(t, a, "a", true, "", 401)
	if a.verifiedManagementKey.valid {
		t.Fatal("failures populated memo")
	}
	a.SetLocalPassword("local")
	a.envSecret = "env"
	assertManagementAuth(t, a, "a", true, "local", 0)
	assertManagementAuth(t, a, "a", false, "env", 0)
	if a.verifiedManagementKey.valid {
		t.Fatal("non-bcrypt success populated memo")
	}
	assertManagementAuth(t, a, "a", true, "correct", 0)
	assertManagementAuth(t, b, "b", true, "correct", 0)
	if a.verifiedManagementKey == b.verifiedManagementKey {
		t.Fatal("handlers must have private entropy")
	}
}

func TestManagementAuthMemoConcurrent(t *testing.T) {
	hashes := []string{authHash(t, "alpha", bcrypt.MinCost), authHash(t, "bravo", bcrypt.MinCost)}
	h := authHandler(hashes[0])
	h.SetLocalPassword("local")
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			for i := range 12 {
				if worker == 0 {
					h.SetConfig(authHandler(hashes[i%2]).cfg)
					continue
				}
				key := "alpha"
				if worker == 2 {
					key = "bravo"
				}
				if worker == 3 {
					key = "wrong"
				}
				h.AuthenticateManagementKey(key+string(rune(i)), true, key)
			}
		})
	}
	wg.Wait()
	h.SetConfig(authHandler(hashes[1]).cfg)
	assertManagementAuth(t, h, "final", true, "alpha", 401)
	assertManagementAuth(t, h, "final", true, "bravo", 0)
}

func BenchmarkManagementAuthHTTP(b *testing.B) {
	hash := authHash(b, "synthetic-http-password", bcrypt.DefaultCost)
	for _, mode := range []string{"Baseline", "Warm", "Cold"} {
		b.Run(mode, func(b *testing.B) {
			h := NewHandler(&config.Config{RemoteManagement: config.RemoteManagement{AllowRemote: true, SecretKey: hash}}, "", nil)
			h.configFilePath = filepath.Join(b.TempDir(), "config.yaml")
			if err := os.WriteFile(h.configFilePath, []byte("config-version: 8\nserver: {port: 8317}\n"), 0600); err != nil {
				b.Fatal(err)
			}
			r := gin.New()
			r.GET("/v8/management/config/*path", h.Middleware(), h.ConfigV8)
			server := httptest.NewServer(r)
			defer server.Close()
			request := func() {
				req, err := http.NewRequest(http.MethodGet, server.URL+"/v8/management/config/server/port", nil)
				if err != nil {
					b.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer synthetic-http-password")
				resp, err := server.Client().Do(req)
				if err != nil {
					b.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || resp.StatusCode != 200 || string(body) != "8317" {
					b.Fatalf("incorrect response %d %s %v", resp.StatusCode, body, err)
				}
			}
			request()
			b.ResetTimer()
			for range b.N {
				if mode != "Warm" {
					h.mu.Lock()
					h.verifiedManagementKey = verifiedManagementKey{}
					if mode == "Cold" {
						rand.Read(h.managementMemoKey[:])
					}
					h.mu.Unlock()
				}
				request()
			}
		})
	}
}

func TestManagementAuthMemoHTTP(t *testing.T) {
	h := authHandler(authHash(t, "correct", bcrypt.MinCost))
	router := gin.New()
	router.GET("/v8/management/credentials", h.Middleware(), func(c *gin.Context) { c.JSON(200, gin.H{"files": []string{"synthetic"}}) })
	request := func(key string, want int) {
		t.Helper()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v8/management/credentials", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer "+key)
		router.ServeHTTP(recorder, req)
		if recorder.Code != want {
			t.Fatalf("HTTP status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), want)
		}
		if want == 200 && recorder.Body.String() != `{"files":["synthetic"]}` {
			t.Fatal("incorrect handler response")
		}
	}
	request("correct", 200)
	request("correct", 200)
	for range 5 {
		request("wrong", 401)
	}
	request("correct", 403)
}

func TestManagementAuthZeroKeyDisablesMemo(t *testing.T) {
	h := authHandler(authHash(t, "correct", bcrypt.MinCost))
	h.managementMemoKey = [32]byte{}
	assertManagementAuth(t, h, "a", true, "correct", 0)
	assertManagementAuth(t, h, "a", true, "correct", 0)
	if h.verifiedManagementKey.valid {
		t.Fatal("zero MAC key must disable memo")
	}
}
