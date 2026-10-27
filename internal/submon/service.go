package submon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Options struct {
	DataDir    string
	ListenAddr string
}

func Run(ctx context.Context, o Options) (errRun error) {
	addr := o.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:8318"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("listen address must include a loopback host and port")
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("monitor must listen on loopback")
	}
	s, err := openStore(o.DataDir)
	if err != nil {
		return err
	}
	defer func() { errRun = errors.Join(errRun, s.close()) }()
	key, err := loadOrCreateKey(filepath.Join(o.DataDir, "access.key"))
	if err != nil {
		return err
	}
	n, err := localAccess()
	if err != nil {
		return err
	}
	client := quotaClient()
	defer client.CloseIdleConnections()
	reader := quotaReader{native: n, client: client}
	c := newCollector(s, reader.sources(), time.Now)
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("listen for monitor: %w", err)
	}
	server := &http.Server{Handler: newAPI(s, c, key, time.Now, listener.Addr().String())}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	collected := make(chan error, 1)
	served := make(chan error, 1)
	go func() { collected <- c.run(ctx) }()
	go func() { served <- server.Serve(listener) }()
	var result error
	collectorDone := false
	select {
	case <-ctx.Done():
	case result = <-collected:
		collectorDone = true
	case result = <-served:
		if errors.Is(result, http.ErrServerClosed) {
			result = nil
		}
	}
	cancel()
	if errClose := server.Close(); errClose != nil {
		result = errors.Join(result, errClose)
	}
	if !collectorDone {
		result = errors.Join(result, <-collected)
	}
	return result
}
func loadOrCreateKey(path string) (string, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("monitor access key must be a private regular file")
		}
		b, errRead := os.ReadFile(path)
		if errRead != nil {
			return "", errRead
		}
		key := strings.TrimSpace(string(b))
		decoded, errHex := hex.DecodeString(key)
		if errHex != nil || len(decoded) != 32 {
			return "", errors.New("monitor access key has an invalid format")
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	key := hex.EncodeToString(b)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, errWrite := f.WriteString(key + "\n")
	errClose := f.Close()
	return key, errors.Join(errWrite, errClose)
}
