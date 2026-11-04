package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadBazarrKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(p, []byte("analytics:\n  enabled: false\nauth:\n  password: ''\n  type: null\n  apikey: 0123456789abcdef0123456789abcdef\n  username: ''\nbackup:\n  day: 6\n"), 0o600)
	if k := readBazarrKey(p); k != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("key %q", k)
	}
	if k := readBazarrKey(filepath.Join(t.TempDir(), "missing.yaml")); k != "" {
		t.Fatalf("missing file gave %q", k)
	}
}
