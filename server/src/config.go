package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config mirrors the [server] section of anytable.conf.
type Config struct {
	Port        int
	PIN         string
	FPSTarget   int
	JPEGQuality int
	Autostart   bool
}

// DefaultConfig matches the shipped server/config/anytable.conf.
func DefaultConfig() Config {
	return Config{Port: 8443, PIN: "", FPSTarget: 60, JPEGQuality: 80, Autostart: true}
}

// Preset is a GUI quality preset.
type Preset struct {
	Name    string
	FPS     int
	Quality int
}

// Presets: Performance 60 FPS / Balanced 45 FPS / High 30 FPS (higher JPEG quality as FPS drops).
var Presets = []Preset{
	{"performance", 60, 70},
	{"balanced", 45, 80},
	{"high", 30, 90},
}

// PresetByName looks a preset up by name.
func PresetByName(name string) (Preset, bool) {
	for _, p := range Presets {
		if p.Name == strings.ToLower(name) {
			return p, true
		}
	}
	return Preset{}, false
}

// PresetName returns the preset matching the configured FPS, or "custom".
func (c Config) PresetName() string {
	for _, p := range Presets {
		if p.FPS == c.FPSTarget {
			return p.Name
		}
	}
	return "custom"
}

// Validate rejects values the engine cannot honour.
func (c Config) Validate() error {
	switch {
	case c.Port < 1 || c.Port > 65535:
		return fmt.Errorf("port must be 1-65535, got %d", c.Port)
	case c.Port == AdminPort || c.Port == GUIPort:
		return fmt.Errorf("port %d is reserved for the local settings interface", c.Port)
	case c.FPSTarget < 1 || c.FPSTarget > 60:
		return fmt.Errorf("fps_target must be 1-60, got %d", c.FPSTarget)
	case c.JPEGQuality < 1 || c.JPEGQuality > 100:
		return fmt.Errorf("jpeg_quality must be 1-100, got %d", c.JPEGQuality)
	}
	return validPIN(c.PIN)
}

// validPIN: empty (no PIN) or 4-32 letters/digits.
func validPIN(pin string) error {
	if pin == "" {
		return nil
	}
	if len(pin) < 4 || len(pin) > 32 {
		return errors.New("pin must be empty or 4-32 characters")
	}
	for _, r := range pin {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return errors.New("pin may only contain letters and digits")
		}
	}
	return nil
}

// LoadConfig reads path; a missing file yields the defaults.
func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	c, err := parseConfig(f, DefaultConfig())
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func parseConfig(r io.Reader, c Config) (Config, error) {
	sc := bufio.NewScanner(r)
	section := ""
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' && line[len(line)-1] == ']' {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("line %d: expected key = value", n)
		}
		if section != "server" {
			continue
		}
		key, val := strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		var err error
		switch key {
		case "port":
			c.Port, err = strconv.Atoi(val)
		case "pin":
			c.PIN = val
		case "fps_target":
			c.FPSTarget, err = strconv.Atoi(val)
		case "jpeg_quality":
			c.JPEGQuality, err = strconv.Atoi(val)
		case "autostart":
			c.Autostart, err = strconv.ParseBool(val)
		}
		if err != nil {
			return c, fmt.Errorf("line %d: invalid %s %q", n, key, val)
		}
	}
	return c, sc.Err()
}

// Save writes the config atomically (temp file + rename) with 0600 permissions (it holds the PIN).
func (c Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".anytable-conf-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = fmt.Fprintf(tmp, "[server]\nport = %d\npin = %q\nfps_target = %d\njpeg_quality = %d\nautostart = %t\n",
		c.Port, c.PIN, c.FPSTarget, c.JPEGQuality, c.Autostart)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
