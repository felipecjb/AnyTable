// Package launcher implements the connection dialog UI and persistent Recent Connections store.
package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPort = 8443
	maxRecent   = 8
)

// Entry represents a previously used remote server endpoint (PIN is never persisted).
type Entry struct {
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	LastUsed time.Time `json:"last_used"`
}

// Label formats the entry for display in the Recent Connections dropdown.
func (e Entry) Label() string {
	return fmt.Sprintf("%s:%d", e.Host, e.Port)
}

// Store persists recent connections in local user configuration storage.
type Store struct {
	path    string
	entries []Entry
}

// DefaultPath returns the OS-standard per-user path for AnyTable client settings.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "anytable", "recent.json")
}

// Load reads recent connections from path (missing file yields an empty store).
func Load(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s.entries); err != nil {
		return &Store{path: path}, err
	}
	s.sortAndTrim()
	return s, nil
}

// Entries returns a copy of recent entries sorted most-recent first.
func (s *Store) Entries() []Entry {
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Add inserts or updates host:port as most recent and saves to disk.
func (s *Store) Add(host string, port int) error {
	host = strings.TrimSpace(host)
	if host == "" || port < 1 || port > 65535 {
		return errors.New("invalid host or port")
	}
	now := time.Now().UTC()
	updated := false
	for i := range s.entries {
		if strings.EqualFold(s.entries[i].Host, host) && s.entries[i].Port == port {
			s.entries[i].Host = host
			s.entries[i].LastUsed = now
			updated = true
			break
		}
	}
	if !updated {
		s.entries = append(s.entries, Entry{Host: host, Port: port, LastUsed: now})
	}
	s.sortAndTrim()
	return s.Save()
}

func (s *Store) sortAndTrim() {
	sort.SliceStable(s.entries, func(i, j int) bool {
		return s.entries[i].LastUsed.After(s.entries[j].LastUsed)
	})
	if len(s.entries) > maxRecent {
		s.entries = s.entries[:maxRecent]
	}
}

// Save writes the recent connections file atomically with private permissions.
func (s *Store) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".recent-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Validate validates host and port strings entered in the launcher UI.
func Validate(host, portStr string) (string, int, error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return "", 0, errors.New("host IP / hostname is required")
	}
	pStr := strings.TrimSpace(portStr)
	if pStr == "" {
		return h, DefaultPort, nil
	}
	port, err := strconv.Atoi(pStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("port must be between 1 and 65535")
	}
	return h, port, nil
}
