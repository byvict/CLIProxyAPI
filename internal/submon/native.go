package submon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"golang.org/x/text/unicode/norm"
)

var (
	errCredentialMissing = errors.New("credential_missing")
	errCredentialAPIKey  = errors.New("api_key")
	errUnsupportedSource = errors.New("unsupported_source")
)

type nativeAccess struct {
	home     string
	getenv   func(string) string
	lookup   func(string) (string, bool)
	readFile func(string) ([]byte, error)
	keychain func(context.Context, string, string) ([]byte, error)
	platform string
}

func localAccess() (nativeAccess, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nativeAccess{}, err
	}
	return nativeAccess{home: home, getenv: os.Getenv, lookup: os.LookupEnv, readFile: os.ReadFile, keychain: readKeychain, platform: runtime.GOOS}, nil
}
func readKeychain(ctx context.Context, service, account string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, errCredentialMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"find-generic-password", "-s", service, "-w"}
	if account != "" {
		args = append(args, "-a", account)
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
	data, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return nil, errCredentialMissing
		}
		return nil, errors.New("keychain_unavailable")
	}
	return data, nil
}
func (n nativeAccess) claudeLocation() (string, string) {
	dir := n.getenv("CLAUDE_CONFIG_DIR")
	hasCustom := dir != ""
	if dir == "" {
		dir = filepath.Join(n.home, ".claude")
	}
	if secure, found := n.lookup("CLAUDE_SECURESTORAGE_CONFIG_DIR"); found {
		dir = secure
		hasCustom = secure != ""
		if dir == "" {
			dir = filepath.Join(n.home, ".claude")
		}
	}
	dir = norm.NFC.String(dir)
	service := "Claude Code-credentials"
	if hasCustom {
		sum := sha256.Sum256([]byte(dir))
		service += "-" + hex.EncodeToString(sum[:4])
	}
	return filepath.Join(dir, ".credentials.json"), service
}
func (n nativeAccess) acquire(ctx context.Context, p provider) (credential, error) {
	var b []byte
	var err error
	switch p {
	case codex:
		dir := n.getenv("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(n.home, ".codex")
		}
		b, err = n.readFile(filepath.Join(dir, "auth.json"))
	case grok:
		b, err = n.readFile(filepath.Join(n.home, ".grok", "auth.json"))
	case claude:
		if access := n.getenv("CLAUDE_CODE_OAUTH_TOKEN"); access != "" {
			return credential{Access: access}, nil
		}
		if n.getenv("ANTHROPIC_API_KEY") != "" || n.getenv("CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR") != "" {
			return credential{}, errUnsupportedSource
		}
		path, service := n.claudeLocation()
		if n.platform == "darwin" {
			account := n.getenv("USER")
			if account == "" {
				if u, errUser := user.Current(); errUser == nil {
					account = u.Username
				}
			}
			if !regexp.MustCompile(`^[a-zA-Z0-9._-]+$`).MatchString(account) {
				account = "claude-code-user"
			}
			b, err = n.keychain(ctx, service, account)
		} else {
			err = errCredentialMissing
		}
		if errors.Is(err, errCredentialMissing) {
			b, err = n.readFile(path)
		}
	case muse:
		path := n.getenv("MUSE_AUTH_PATH")
		if path != "" {
			b, err = n.readFile(path)
			break
		}
		if n.platform == "darwin" {
			b, err = n.keychain(ctx, "ai.meta.dev.credentials", "meta")
		} else {
			err = errCredentialMissing
		}
		if errors.Is(err, errCredentialMissing) {
			dir := n.getenv("XDG_CONFIG_HOME")
			if dir == "" {
				dir = filepath.Join(n.home, ".config")
			}
			b, err = n.readFile(filepath.Join(dir, "muse", "auth.json"))
		}
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errCredentialMissing) {
			return credential{}, errCredentialMissing
		}
		return credential{}, errors.New("credential_read")
	}
	return parseCredential(p, b)
}
func acquisitionFailure(err error) missing {
	if errors.Is(err, errCredentialMissing) {
		return missing{Status: unavailable, Code: "credential_missing"}
	}
	switch {
	case errors.Is(err, errCredentialAPIKey):
		return missing{Status: unavailable, Code: "api_key"}
	case errors.Is(err, errUnsupportedSource):
		return missing{Status: unavailable, Code: "unsupported_source"}
	}
	return missing{Status: invalidResponse, Code: "credential_read"}
}
