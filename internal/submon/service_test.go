package submon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRejectsNonLoopbackBeforeCreatingRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unused")
	if err := Run(context.Background(), Options{DataDir: root, ListenAddr: "0.0.0.0:8318"}); err == nil {
		t.Fatal("public listener accepted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("invalid listener created runtime files")
	}
}
func TestCanceledLifecycleReleasesOwnerAndPreservesPrivateKey(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, Options{DataDir: root, ListenAddr: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "access.key")
	first, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateKey(path)
	if err != nil || first != second {
		t.Fatal("startup key did not survive restart")
	}
	s, err := openStore(root)
	if err != nil {
		t.Fatal("runtime owner leaked", err)
	}
	if err = s.close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe key permissions")
	}
}
