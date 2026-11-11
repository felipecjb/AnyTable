package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseShippedConfig(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "config", "anytable.conf"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseConfig(strings.NewReader(string(raw)), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if c != DefaultConfig() {
		t.Fatalf("shipped config %+v != defaults %+v", c, DefaultConfig())
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "anytable.conf")
	want := Config{Port: 9000, PIN: "4829", FPSTarget: 45, JPEGQuality: 85, Autostart: false}
	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestLoadMissingConfigGivesDefaults(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "nope.conf"))
	if err != nil || c != DefaultConfig() {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{Port: 0, FPSTarget: 60, JPEGQuality: 80},
		{Port: AdminPort, FPSTarget: 60, JPEGQuality: 80},
		{Port: 8443, FPSTarget: 0, JPEGQuality: 80},
		{Port: 8443, FPSTarget: 61, JPEGQuality: 80},
		{Port: 8443, FPSTarget: 60, JPEGQuality: 101},
		{Port: 8443, FPSTarget: 60, JPEGQuality: 80, PIN: "12"},
		{Port: 8443, FPSTarget: 60, JPEGQuality: 80, PIN: `12"34`},
	}
	for _, c := range bad {
		if c.Validate() == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
	if _, err := parseConfig(strings.NewReader("[server]\nport = abc\n"), DefaultConfig()); err == nil {
		t.Error("expected parse error for non-numeric port")
	}
}

func TestPresets(t *testing.T) {
	for _, p := range Presets {
		c := DefaultConfig()
		c.FPSTarget = p.FPS
		if c.PresetName() != p.Name {
			t.Errorf("preset %s not recognised from fps %d", p.Name, p.FPS)
		}
	}
	c := DefaultConfig()
	c.FPSTarget = 20
	if c.PresetName() != "custom" {
		t.Error("expected custom")
	}
}
