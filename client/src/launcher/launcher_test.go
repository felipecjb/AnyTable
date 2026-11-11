package launcher

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreAddDeduplicateAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anytable", "recent.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Entries()) != 0 {
		t.Fatal("new store should be empty")
	}

	if err := s.Add("192.168.1.10", 8443); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := s.Add("10.0.0.5", 9000); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	// Re-adding existing entry updates timestamp and moves it to top without duplicating.
	if err := s.Add("192.168.1.10", 8443); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Entries()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Host != "192.168.1.10" || got[0].Port != 8443 || got[1].Host != "10.0.0.5" {
		t.Fatalf("unexpected ordering: %+v", got)
	}
}

func TestValidateInput(t *testing.T) {
	if h, p, err := Validate(" 192.168.0.2 ", ""); err != nil || h != "192.168.0.2" || p != DefaultPort {
		t.Fatalf("default port failed: %q %d %v", h, p, err)
	}
	if _, _, err := Validate("", "8443"); err == nil {
		t.Fatal("empty host must fail")
	}
	if _, _, err := Validate("host", "70000"); err == nil {
		t.Fatal("out of range port must fail")
	}
	if _, _, err := Validate("host", "abc"); err == nil {
		t.Fatal("non-numeric port must fail")
	}
}
