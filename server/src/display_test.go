package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindAuthorityFiles(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, age time.Duration) string {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		return p
	}
	home := mk("home/alice/.Xauthority", time.Hour)
	gdm := mk("run/user/1000/gdm/Xauthority", time.Minute)
	old := mk("home/bob/.Xauthority", 48*time.Hour)
	if err := os.MkdirAll(filepath.Join(root, "home", "carol", ".Xauthority"), 0o755); err != nil { // a directory must be ignored
		t.Fatal(err)
	}

	got := findAuthorityFiles(root, "")
	want := []string{gdm, home, old}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order: got %v want %v", got, want)
		}
	}

	withEnv := findAuthorityFiles(root, "/custom/auth")
	if withEnv[0] != "/custom/auth" || len(withEnv) != 4 {
		t.Fatalf("env path must come first: %v", withEnv)
	}
}
