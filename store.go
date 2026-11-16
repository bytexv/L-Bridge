package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const version = "0.4.0"

type Message struct {
	Seq     int    `json:"seq"`
	Agent   string `json:"agent"`
	Kind    string `json:"kind"`
	Body    string `json:"body"`
	Summary string `json:"summary"`
	Time    string `json:"time"`
}
type Agent struct {
	Role string `json:"role"`
	Seen string `json:"seen"`
}
type Task struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Owner    string `json:"owner"`
	Status   string `json:"status"`
	Lease    string `json:"lease,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}
type Artifact struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"`
	Bytes int    `json:"bytes"`
	Agent string `json:"agent"`
	Time  string `json:"time"`
}
type Goal struct {
	WebAccess      bool              `json:"web_access"`
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Prompt         string            `json:"prompt"`
	Root           string            `json:"root"`
	Status         string            `json:"status"`
	Created        string            `json:"created"`
	Demo           bool              `json:"demo"`
	Mode           string            `json:"mode"`
	Summary        string            `json:"summary"`
	SummaryVersion int               `json:"summary_version"`
	SummaryBy      string            `json:"summary_by"`
	Messages       []Message         `json:"messages"`
	Agents         map[string]Agent  `json:"agents"`
	Tasks          []Task            `json:"tasks"`
	Artifacts      []Artifact        `json:"artifacts"`
	Limit          int               `json:"limit"`
	Actions        int               `json:"actions"`
	BytesSent      int64             `json:"bytes_sent"`
	Receipts       map[string]int    `json:"receipts"`
	Votes          map[string]string `json:"votes"`
	MCPServers     []string          `json:"mcp_servers"`
	RunHistory     []*TeamRun        `json:"run_history,omitempty"`
	Team           *TeamRun          `json:"team,omitempty"`
}
type State struct {
	Version     int               `json:"version"`
	Goals       []*Goal           `json:"goals"`
	Settings    Settings          `json:"settings"`
	MCPWarnings map[string]string `json:"mcp_warnings,omitempty"`
}
type Store struct {
	mu      sync.Mutex
	dir     string
	state   State
	changed chan struct{}
	runtime *Runtime
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func id() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func newStore(dir string) (*Store, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	dir = absolute
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, state: State{Version: 1, Goals: []*Goal{}}, changed: make(chan struct{})}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil {
		if err = json.Unmarshal(b, &s.state); err != nil {
			return nil, fmt.Errorf("cannot read state.json; preserve it and restore a backup: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.state.Settings.defaults()
	if s.state.MCPWarnings == nil {
		s.state.MCPWarnings = map[string]string{}
	}
	for _, g := range s.state.Goals {
		if g.Team != nil && g.Team.Status == "running" {
			g.Team.Status = "interrupted"
			g.Team.Error = "L-Bridge restarted. Saved progress is available. Review any interrupted edits, then use Continue task."
			g.Status = "paused"
		}
	}
	s.runtime = newRuntime(s)
	return s, nil
}
func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".lbridge-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	// Keep one known-valid snapshot for manual recovery if storage is damaged.
	if previous, e := os.ReadFile(filepath.Join(s.dir, "state.json")); e == nil && json.Valid(previous) {
		if e = atomicWrite(filepath.Join(s.dir, "state.json.bak"), previous); e != nil {
			return fmt.Errorf("could not save recovery backup: %w", e)
		}
	}
	if err = atomicWrite(filepath.Join(s.dir, "state.json"), b); err != nil {
		return err
	}
	close(s.changed)
	s.changed = make(chan struct{})
	return nil
}
func (s *Store) get(goalID string) (*Goal, error) {
	for _, g := range s.state.Goals {
		if g.ID == goalID {
			return g, nil
		}
	}
	return nil, errors.New("goal not found; use bridge_goals")
}
func (g *Goal) add(agent, kind, body, summary string) Message {
	m := Message{len(g.Messages) + 1, agent, kind, body, summary, now()}
	g.Messages = append(g.Messages, m)
	return m
}
func (g *Goal) active() error {
	if g.Demo {
		return errors.New("demo workspace is read-only for agents")
	}
	if g.Status != "active" {
		return fmt.Errorf("goal is %s; ask the user to resume it in L-Bridge", g.Status)
	}
	if g.Actions >= g.Limit {
		return errors.New("action allowance reached; user can extend it in L-Bridge")
	}
	return nil
}
func (g *Goal) bump() {
	g.Actions++
	if g.Actions >= g.Limit {
		g.Status = "paused"
		g.add("system", "status", "Action allowance reached. Resume with a larger allowance to continue.", "Paused at the action allowance.")
	}
}
func (s *Store) create(title, prompt, root, mode string, limit int) (*Goal, error) {
	title = strings.TrimSpace(title)
	prompt = strings.TrimSpace(prompt)
	if title == "" || len(title) > 120 || prompt == "" || len(prompt) > 32000 {
		return nil, errors.New("provide a title (1–120 bytes) and goal (1–32000 bytes)")
	}
	if mode != "files" && mode != "commands" && mode != "read" {
		return nil, errors.New("invalid workspace access mode")
	}
	if limit < 10 || limit > 10000 {
		return nil, errors.New("action allowance must be 10–10000")
	}
	gid := id()[:12]
	if root == "" {
		root = filepath.Join(s.dir, "workspaces", gid)
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace folder: %w", err)
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return nil, errors.New("workspace must be an existing directory")
	}
	g := &Goal{ID: gid, Title: title, Prompt: prompt, Root: root, Mode: mode, Status: "active", Created: now(), Messages: []Message{}, Agents: map[string]Agent{}, Tasks: []Task{}, Artifacts: []Artifact{}, Limit: limit, Receipts: map[string]int{}, Votes: map[string]string{}}
	g.add("you", "goal", prompt, "Goal created. Agents can choose their roles.")
	s.state.Goals = append(s.state.Goals, g)
	return g, s.save()
}

// Resolve only ordinary relative files. Symlinks and Windows alternate data streams are rejected.
func safePath(root, rel string) (string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || strings.ContainsAny(rel, "\\:\x00") {
		return "", errors.New("use a relative path with forward slashes")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("path leaves workspace")
	}
	parts := strings.Split(clean, string(os.PathSeparator))
	p := root
	for _, part := range parts {
		if part == "." {
			continue
		}
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", errors.New("unsupported path component")
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return "", errors.New("reserved device path")
		}
		p = filepath.Join(p, part)
		st, err := os.Lstat(p)
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlinks are not allowed in workspace file tools")
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return p, nil
}
