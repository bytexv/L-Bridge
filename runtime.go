package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Settings struct {
	AutoSwitch  bool           `json:"-"`
	WebAccess   bool           `json:"-"` // Per-run snapshot of the user-owned goal setting.
	Claude      string         `json:"claude"`
	Codex       string         `json:"codex"`
	ClaudeModel string         `json:"claude_model"`
	CodexModel  string         `json:"codex_model"`
	Rounds      int            `json:"rounds"`
	TurnSeconds int            `json:"turn_seconds"`
	Servers     []ServerConfig `json:"servers"`
}
type ServerConfig struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func (c *Settings) defaults() {
	if c.Claude == "" {
		c.Claude = "claude"
	}
	if c.Codex == "" {
		c.Codex = "codex"
	}
	if c.Rounds == 0 {
		c.Rounds = 8
	}
	if c.TurnSeconds == 0 {
		c.TurnSeconds = 300
	}
	if c.Servers == nil {
		c.Servers = []ServerConfig{}
	}
}
func (c Settings) validate() error {
	if c.Rounds < 1 || c.Rounds > 40 || c.TurnSeconds < 30 || c.TurnSeconds > 900 {
		return errors.New("use 1–40 team rounds and 30–900 seconds per agent turn")
	}
	for _, v := range []string{c.Claude, c.Codex, c.ClaudeModel, c.CodexModel} {
		if len(v) > 2000 || strings.ContainsRune(v, 0) {
			return errors.New("invalid runner setting")
		}
	}
	if len(c.Servers) > 12 {
		return errors.New("at most 12 MCP servers")
	}
	seen := map[string]bool{}
	for _, server := range c.Servers {
		if !validID(server.ID) || seen[server.ID] || strings.TrimSpace(server.Name) == "" || len(server.Name) > 80 || server.Command == "" || len(server.Command) > 2000 || len(server.Args) > 64 {
			return errors.New("MCP servers need a unique lowercase ID, name, command, and at most 64 arguments")
		}
		seen[server.ID] = true
		for _, v := range append([]string{server.Command}, server.Args...) {
			if len(v) > 8000 || strings.ContainsRune(v, 0) {
				return errors.New("invalid MCP command argument")
			}
		}
	}
	return nil
}
func validID(s string) bool {
	if len(s) < 1 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

type Runtime struct {
	s      *Store
	mu     sync.Mutex
	runs   map[string]context.CancelFunc
	tokens map[string]RunnerScope
	hub    *MCPHub
}
type RunnerScope struct{ Goal, Agent string }

func newRuntime(s *Store) *Runtime {
	rt := &Runtime{s: s, runs: map[string]context.CancelFunc{}, tokens: map[string]RunnerScope{}, hub: newMCPHub()}
	for sid, warning := range s.state.MCPWarnings {
		rt.hub.connection(sid).uncertain = warning
	}
	return rt
}
func (rt *Runtime) close() {
	rt.mu.Lock()
	for _, cancel := range rt.runs {
		cancel()
	}
	rt.tokens = map[string]RunnerScope{}
	rt.mu.Unlock()
	rt.hub.close()
}
func (rt *Runtime) stop(gid string) {
	rt.mu.Lock()
	if cancel := rt.runs[gid]; cancel != nil {
		cancel()
	}
	for token, scope := range rt.tokens {
		if scope.Goal == gid {
			delete(rt.tokens, token)
		}
	}
	rt.mu.Unlock()
	cancelGoal(gid)
}
func (rt *Runtime) scope(token string) (RunnerScope, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	scope, ok := rt.tokens[token]
	return scope, ok
}
func (rt *Runtime) busy() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.runs) > 0
}
func cloneSettings(c Settings) Settings {
	b, _ := json.Marshal(c)
	var out Settings
	json.Unmarshal(b, &out)
	return out
}

func (app *App) runtimeUI(r *http.Request, a M) (any, error) {
	s, rt := app.s, app.s.runtime
	switch r.URL.Path {
	case "/api/team/start", "/api/team/continue":
		return rt.start(str(a, "goal_id"))
	case "/api/team/stop":
		s.mu.Lock()
		g, err := s.get(str(a, "goal_id"))
		if err == nil {
			if g.Demo {
				err = errors.New("demo is read-only")
			} else {
				g.Status = "paused"
				g.add("you", "status", "Team stopped by user.", "You stopped the team.")
				err = s.save()
			}
		}
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		rt.stop(str(a, "goal_id"))
		return M{"ok": true}, nil
	case "/api/runner/settings":
		b, _ := json.Marshal(a)
		var cfg Settings
		if err := json.Unmarshal(b, &cfg); err != nil {
			return nil, errors.New("invalid settings")
		}
		cfg.defaults()
		if err := cfg.validate(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if rt.busy() {
			return nil, errors.New("stop the team before changing connections")
		}
		if rt.hub.busy() {
			return nil, errors.New("an MCP operation is active; wait or pause its goal")
		}
		rt.hub.close()
		s.state.Settings = cfg
		return M{"ok": true}, s.save()
	case "/api/runner/update":
		if rt.busy() || rt.hub.busy() || activeCommands() {
			return nil, errors.New("stop the team and wait for tools to finish before updating")
		}
		return M{"ok": true}, launchUpdate(s.dir)
	case "/api/runner/setup":
		return app.setupAgent(a)
	case "/api/runner/check":
		s.mu.Lock()
		cfg := cloneSettings(s.state.Settings)
		s.mu.Unlock()
		results := make(chan M, 2)
		for _, item := range []struct{ agent, path string }{{"claude", cfg.Claude}, {"chatgpt", cfg.Codex}} {
			go func(agent, path string) { results <- probeRunner(r.Context(), agent, path) }(item.agent, item.path)
		}
		return M{"checks": []M{<-results, <-results}}, nil
	case "/api/mcp/preset":
		cfg := ServerConfig{ID: "roblox", Name: "Roblox Studio"}
		switch runtime.GOOS {
		case "windows":
			path := filepath.Join(os.Getenv("LOCALAPPDATA"), "Roblox", "mcp.bat")
			if strings.ContainsAny(path, "\"%!\r\n") {
				return nil, errors.New("set the Roblox launcher path manually")
			}
			cfg.Command = "cmd.exe"
			cfg.Args = []string{"/d", "/s", "/c", `""` + path + `""`}
		case "darwin":
			cfg.Command = "/Applications/RobloxStudio.app/Contents/MacOS/StudioMCP"
			cfg.Args = []string{}
		default:
			return nil, errors.New("Roblox Studio preset is available on Windows and macOS")
		}
		return cfg, nil
	case "/api/mcp/test":
		s.mu.Lock()
		var cfg *ServerConfig
		for _, c := range s.state.Settings.Servers {
			if c.ID == str(a, "server_id") {
				copy := c
				cfg = &copy
				break
			}
		}
		s.mu.Unlock()
		if cfg == nil {
			return nil, errors.New("save this MCP connection first")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		return rt.hub.list(ctx, *cfg)
	case "/api/mcp/reset":
		if rt.busy() {
			return nil, errors.New("stop the team before reconnecting a server")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := rt.hub.reset(str(a, "server_id")); err != nil {
			return nil, err
		}
		delete(s.state.MCPWarnings, str(a, "server_id"))
		return M{"ok": true}, s.save()
	case "/api/mcp/allow":
		s.mu.Lock()
		defer s.mu.Unlock()
		g, err := s.get(str(a, "goal_id"))
		if err != nil {
			return nil, err
		}
		if g.Demo {
			return nil, errors.New("demo is read-only")
		}
		if rt.busy() {
			return nil, errors.New("stop the team before changing goal connections")
		}
		if rt.hub.busy() {
			return nil, errors.New("an MCP operation is active")
		}
		b, _ := json.Marshal(a["server_ids"])
		var ids []string
		if err = json.Unmarshal(b, &ids); err != nil {
			return nil, errors.New("server_ids must be an array")
		}
		seen := map[string]bool{}
		for _, sid := range ids {
			found := false
			for _, c := range s.state.Settings.Servers {
				if c.ID == sid {
					found = true
				}
			}
			if !found || seen[sid] {
				return nil, errors.New("invalid or duplicate server ID")
			}
			seen[sid] = true
		}
		g.MCPServers = ids
		rt.hub.releaseGoal(g.ID, "")
		return M{"ok": true}, s.save()
	}
	return nil, errors.New("unknown runtime route")
}

// Invoke executables directly. For the standard npm Codex shim on Windows,
// resolve the known JS entrypoint and invoke node, avoiding cmd.exe quoting.
func resolveRunner(path, agent string) (string, []string, error) {
	p, err := exec.LookPath(path)
	if runtime.GOOS == "windows" && path == "claude" {
		home, _ := os.UserHomeDir()
		if native, e := exec.LookPath(filepath.Join(home, ".local", "bin", "claude.exe")); e == nil {
			p, err = native, nil
		}
	}
	if err != nil {
		if !filepath.IsAbs(path) && (path == "claude" || path == "codex") {
			home, _ := os.UserHomeDir()
			candidates := []string{filepath.Join(home, ".local", "bin", path), filepath.Join(home, ".cargo", "bin", path)}
			if runtime.GOOS == "windows" {
				candidates = append(candidates, filepath.Join(home, ".local", "bin", path+".exe"), filepath.Join(os.Getenv("APPDATA"), "npm", path+".cmd"))
			}
			for _, candidate := range candidates {
				if found, e := exec.LookPath(candidate); e == nil {
					p = found
					err = nil
					break
				}
			}
		}
		if err != nil {
			return "", nil, fmt.Errorf("%s runner not found; install and sign in, or set its full executable path", agent)
		}
	}
	p, _ = filepath.Abs(p)
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(p), ".cmd") && (agent == "chatgpt" || agent == "claude") {
		js := filepath.Join(filepath.Dir(p), "node_modules", "@openai", "codex", "bin", "codex.js")
		if agent == "claude" {
			js = filepath.Join(filepath.Dir(p), "node_modules", "@anthropic-ai", "claude-code", "cli.js")
		}
		if _, err = os.Stat(js); err != nil {
			return "", nil, errors.New("unrecognized npm shim; choose the native agent executable")
		}
		node, err := exec.LookPath("node")
		if err != nil && runtime.GOOS == "windows" {
			node, err = exec.LookPath(filepath.Join(os.Getenv("ProgramFiles"), "nodejs", "node.exe"))
		}
		if err != nil {
			return "", nil, errors.New("Node.js is required for this npm installation")
		}
		return node, []string{js}, nil
	}
	if strings.EqualFold(filepath.Ext(p), ".cmd") || strings.EqualFold(filepath.Ext(p), ".bat") {
		return "", nil, errors.New("choose the native runner executable instead of a shell script")
	}
	return p, nil, nil
}
func probeRunner(ctx context.Context, agent, path string) M {
	out := M{"agent": agent, "installed": false, "authenticated": false}
	exe, prefix, err := resolveRunner(path, agent)
	if err != nil {
		out["message"] = err.Error()
		return out
	}
	run := func(args ...string) (string, error) {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(c, exe, append(append([]string{}, prefix...), args...)...)
		prepareProcess(cmd)
		buf := &cappedBuffer{}
		cmd.Stdout = buf
		cmd.Stderr = buf
		cmd.WaitDelay = time.Second
		e := cmd.Run()
		killDescendants(cmd)
		return strings.TrimSpace(string(buf.data)), e
	}
	v, err := run("--version")
	if err != nil {
		out["message"] = "Could not run this executable: " + clip(v, 500)
		return out
	}
	out["installed"] = true
	out["path"] = exe
	lines := strings.Split(v, "\n")
	out["version"] = clip(lines[len(lines)-1], 200)
	args := []string{"auth", "status"}
	if agent == "chatgpt" {
		args = []string{"login", "status"}
	}
	_, err = run(args...)
	out["authenticated"] = err == nil
	if err != nil {
		out["message"] = "Sign in using " + map[string]string{"claude": "claude auth login", "chatgpt": "codex login"}[agent] + " in a terminal, then check again."
	} else {
		out["message"] = "Installed and signed in. Account access and usage limits still apply."
	}
	return out
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
