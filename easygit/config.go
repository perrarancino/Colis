package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Config struct {
	Folder      string `json:"folder"`
	Repo        string `json:"repo"` // owner/name
	Branch      string `json:"branch"`
	Message     string `json:"message"`
	User        string `json:"user"`
	AutoSync    bool   `json:"autoSync"` // commit on a timer
	IntervalMin int    `json:"intervalMin"`
	Autostart   bool   `json:"autostart"`
	SyncOnStart bool   `json:"syncOnStart"` // pull from GitHub when the program starts
	Tray        bool   `json:"tray"`
}

func dataDir() string {
	base, err := os.UserConfigDir() // %APPDATA%
	if err != nil {
		base = "."
	}
	d := filepath.Join(base, "EasyGit")
	_ = os.MkdirAll(d, 0o700)
	return d
}

func LoadConfig() Config {
	c := Config{Tray: true, IntervalMin: 10, SyncOnStart: true}
	if b, err := os.ReadFile(filepath.Join(dataDir(), "config.json")); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func (c Config) Save() error {
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(filepath.Join(dataDir(), "config.json"), b, 0o600)
}

// SplitRepo accepts "owner/name", "https://github.com/owner/name(.git)" or
// "git@github.com:owner/name.git".
func SplitRepo(s string) (owner, name string, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/", "git@github.com:"} {
		s = strings.TrimPrefix(s, p)
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ---- token, encrypted with Windows DPAPI (bound to this Windows user) -------------

func tokenPath() string { return filepath.Join(dataDir(), "token.bin") }

func SaveToken(token string) error {
	if token == "" {
		_ = os.Remove(tokenPath())
		return nil
	}
	b := []byte(token)
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	enc := unsafe.Slice(out.Data, out.Size)
	return os.WriteFile(tokenPath(), enc, 0o600)
}

func LoadToken() string {
	enc, err := os.ReadFile(tokenPath())
	if err != nil || len(enc) == 0 {
		return ""
	}
	in := windows.DataBlob{Size: uint32(len(enc)), Data: &enc[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return ""
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	return string(unsafe.Slice(out.Data, out.Size))
}

// ---- per-project sync state -----------------------------------------------------

type Entry struct {
	SHA  string `json:"sha"`
	Mode string `json:"mode"`
}

type FileCache struct {
	MTime int64  `json:"mtime"`
	Size  int64  `json:"size"`
	SHA   string `json:"sha"`           // hash of the content with CRLF normalised to LF
	Raw   string `json:"raw,omitempty"` // hash of the bytes exactly as on disk
}

// State remembers what the last successful sync looked like: it is the "base"
// of the three-way comparison between local files and GitHub.
type State struct {
	Head  string               `json:"head"`
	Tree  string               `json:"tree"`
	Base  map[string]Entry     `json:"base"`
	Cache map[string]FileCache `json:"cache"`
}

func statePath(folder, repo, branch string) string {
	h := sha1.Sum([]byte(strings.ToLower(filepath.Clean(folder)) + "|" + strings.ToLower(repo) + "|" + branch))
	return filepath.Join(dataDir(), "state-"+hex.EncodeToString(h[:6])+".json")
}

func LoadState(folder, repo, branch string) *State {
	s := &State{}
	if b, err := os.ReadFile(statePath(folder, repo, branch)); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Base == nil {
		s.Base = map[string]Entry{}
	}
	if s.Cache == nil {
		s.Cache = map[string]FileCache{}
	}
	return s
}

func (s *State) Save(folder, repo, branch string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if folder == "" {
		return errors.New("no folder")
	}
	return os.WriteFile(statePath(folder, repo, branch), b, 0o600)
}
