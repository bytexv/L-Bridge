package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type MCPHub struct {
	mu      sync.Mutex
	servers map[string]*MCPConnection
}
type MCPConnection struct {
	gate        chan struct{}
	client      *MCPClient
	fingerprint string
	// Fields below are protected by MCPHub.mu; client/gate are separate.
	goal, agent, lease, uncertain string
	expires                       time.Time
	busy                          bool
}

func newMCPHub() *MCPHub { return &MCPHub{servers: map[string]*MCPConnection{}} }
func (h *MCPHub) connection(id string) *MCPConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.servers[id]
	if c == nil {
		c = &MCPConnection{gate: make(chan struct{}, 1)}
		c.gate <- struct{}{}
		h.servers[id] = c
	}
	return c
}
func (h *MCPHub) busy() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.servers {
		if c.busy {
			return true
		}
	}
	return false
}
func (h *MCPHub) snapshot() []M {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []M{}
	for id, c := range h.servers {
		out = append(out, M{"id": id, "busy": c.busy, "goal": c.goal, "agent": c.agent, "lease_until": c.expires.UTC().Format(time.RFC3339), "needs_review": c.uncertain})
	}
	return out
}
func (h *MCPHub) close() {
	// Stop only clients; retain connection objects so callers already waiting
	// on their gates can never create a second transport for the same ID.
	h.mu.Lock()
	items := []*MCPConnection{}
	for _, c := range h.servers {
		items = append(items, c)
	}
	h.mu.Unlock()
	for _, c := range items {
		select {
		case <-c.gate:
			if c.client != nil {
				c.client.close()
				c.client = nil
			}
			h.mu.Lock()
			c.goal = ""
			c.agent = ""
			c.lease = ""
			h.mu.Unlock()
			c.gate <- struct{}{}
		default:
		}
	}
}
func (h *MCPHub) releaseGoal(goal, agent string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.servers {
		if c.goal == goal && (agent == "" || c.agent == agent) && !c.busy {
			c.goal = ""
			c.agent = ""
			c.lease = ""
		}
	}
}
func (h *MCPHub) lock(ctx context.Context, c *MCPConnection) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.gate:
		return nil
	}
}
func (h *MCPHub) connect(ctx context.Context, c *MCPConnection, cfg ServerConfig) error {
	b, _ := json.Marshal(cfg)
	fingerprint := hash(b)
	if c.client != nil && (!c.client.alive() || c.fingerprint != fingerprint) {
		c.client.close()
		c.client = nil
	}
	if c.client == nil {
		init, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		client, err := startMCP(init, cfg)
		if err != nil {
			return err
		}
		c.client = client
		c.fingerprint = fingerprint
	}
	return nil
}
func (h *MCPHub) list(ctx context.Context, cfg ServerConfig) (any, error) {
	c := h.connection(cfg.ID)
	if err := h.lock(ctx, c); err != nil {
		return nil, err
	}
	defer func() { c.gate <- struct{}{} }()
	if err := h.connect(ctx, c, cfg); err != nil {
		return nil, err
	}
	tools, err := c.client.tools(ctx)
	return M{"server_id": cfg.ID, "tools": tools}, err
}
func (h *MCPHub) acquire(server, goal, agent string) (any, error) {
	c := h.connection(server)
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.uncertain != "" {
		return nil, errors.New(c.uncertain)
	}
	if c.busy || (c.lease != "" && time.Now().Before(c.expires) && (c.goal != goal || c.agent != agent)) {
		return M{"acquired": false, "owner": c.agent, "goal": c.goal, "retry": "Do other work or end this turn; never poll for the lease."}, nil
	}
	if c.goal != goal || c.agent != agent || c.lease == "" || time.Now().After(c.expires) {
		c.lease = id()
	}
	c.goal = goal
	c.agent = agent
	c.expires = time.Now().Add(3 * time.Minute)
	return M{"acquired": true, "lease": c.lease, "expires": c.expires.Format(time.RFC3339), "instruction": "Hold across read/edit/test, then release. Each call renews the lease. Re-read state after acquiring a new lease."}, nil
}
func (h *MCPHub) release(server, goal, agent, lease string) error {
	c := h.connection(server)
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.goal != goal || c.agent != agent || c.lease != lease || lease == "" {
		return errors.New("lease does not belong to this agent")
	}
	if c.busy {
		return errors.New("wait for the in-flight call before releasing")
	}
	c.goal = ""
	c.agent = ""
	c.lease = ""
	return nil
}
func (s *Store) mcpConfig(goal, agent, server string) (ServerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.get(goal)
	if err != nil {
		return ServerConfig{}, err
	}
	if _, ok := g.Agents[agent]; !ok {
		return ServerConfig{}, errors.New("join the goal first")
	}
	if err = g.active(); err != nil {
		return ServerConfig{}, err
	}
	if g.Mode != "commands" {
		return ServerConfig{}, errors.New("MCP tools require Files + commands access in this goal")
	}
	for _, allowed := range g.MCPServers {
		if allowed == server {
			for _, cfg := range s.state.Settings.Servers {
				if cfg.ID == server {
					return cfg, nil
				}
			}
		}
	}
	return ServerConfig{}, errors.New("enable this MCP server for the goal in Connections")
}
func (s *Store) mcpCall(ctx context.Context, agent string, a M) (any, error) {
	gid, sid, op := str(a, "goal_id"), str(a, "server_id"), str(a, "op")
	h := s.runtime.hub
	if op == "servers" {
		s.mu.Lock()
		defer s.mu.Unlock()
		g, err := s.get(gid)
		if err != nil {
			return nil, err
		}
		out := []M{}
		for _, cfg := range s.state.Settings.Servers {
			enabled := false
			for _, id := range g.MCPServers {
				if id == cfg.ID {
					enabled = true
				}
			}
			out = append(out, M{"id": cfg.ID, "name": cfg.Name, "enabled_for_goal": enabled})
		}
		return M{"servers": out}, nil
	}
	if op == "result" {
		rid := str(a, "receipt_id")
		if !validID(rid) {
			return nil, errors.New("invalid receipt ID")
		}
		s.mu.Lock()
		g, err := s.get(gid)
		found := false
		if err == nil {
			for _, m := range g.Messages {
				if m.Kind == "mcp_result" && strings.Contains(m.Body, `"receipt_id":"`+rid+`"`) {
					found = true
					break
				}
			}
		}
		s.mu.Unlock()
		if !found {
			return nil, errors.New("receipt not found in this goal")
		}
		b, err := os.ReadFile(filepath.Join(s.dir, "mcp-results", gid, rid+".json"))
		if err != nil {
			return nil, err
		}
		var result M
		if err = json.Unmarshal(b, &result); err != nil {
			return nil, err
		}
		return M{"mcp_result": result, "receipt_id": rid}, nil
	}
	if op == "release" {
		return M{"released": true}, h.release(sid, gid, agent, str(a, "lease"))
	}
	cfg, err := s.mcpConfig(gid, agent, sid)
	if err != nil {
		return nil, err
	}
	switch op {
	case "tools":
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return h.list(c, cfg)
	case "acquire":
		return h.acquire(sid, gid, agent)
	case "call":
	default:
		return nil, errors.New("invalid MCP operation")
	}
	toolName := str(a, "tool")
	args, ok := a["arguments"].(map[string]any)
	if toolName == "" || len(toolName) > 200 || !ok {
		return nil, errors.New("tool name and arguments object required")
	}
	c := h.connection(sid)
	runCtx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	jobID := gid + ":mcp:" + id()
	jobs.Lock()
	jobs.items[jobID] = cancel
	jobs.Unlock()
	defer func() { jobs.Lock(); delete(jobs.items, jobID); jobs.Unlock() }()
	if err = h.lock(runCtx, c); err != nil {
		return nil, err
	}
	defer func() { c.gate <- struct{}{} }()
	// Recheck after waiting; a pause, settings change, or expired lease must
	// never dispatch a stale queued edit.
	if _, err = s.mcpConfig(gid, agent, sid); err != nil {
		return nil, err
	}
	h.mu.Lock()
	if c.uncertain != "" {
		err = errors.New(c.uncertain)
	} else if c.goal != gid || c.agent != agent || c.lease == "" || c.lease != str(a, "lease") || time.Now().After(c.expires) {
		err = errors.New("acquire a current lease and re-read the scene before calling tools")
	}
	if err == nil {
		c.busy = true
	}
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer func() { h.mu.Lock(); c.busy = false; c.expires = time.Now().Add(3 * time.Minute); h.mu.Unlock() }()
	if err = h.connect(runCtx, c, cfg); err != nil {
		return nil, err
	}
	tools, err := c.client.tools(runCtx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, t := range tools {
		if str(t, "name") == toolName {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("tool is not exposed by this MCP server")
	}
	s.mu.Lock()
	g, _ := s.get(gid)
	err = g.active()
	if err == nil && g.Mode != "commands" {
		err = errors.New("MCP access was revoked")
	}
	allowed := false
	for _, sid := range g.MCPServers {
		if sid == cfg.ID {
			allowed = true
		}
	}
	current := false
	expected, _ := json.Marshal(cfg)
	for _, sc := range s.state.Settings.Servers {
		if sc.ID == cfg.ID {
			actual, _ := json.Marshal(sc)
			current = string(actual) == string(expected)
		}
	}
	if err == nil && (!allowed || !current) {
		err = errors.New("MCP configuration changed; discover it again before retrying")
	}
	if err == nil {
		err = runCtx.Err()
	}
	if err == nil {
		g.Votes = map[string]string{}
		g.add(agent, "mcp_call", sid+" / "+toolName, "Using "+cfg.Name+": "+toolName)
		g.bump()
		err = s.save()
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	raw, callErr := c.client.call(runCtx, "tools/call", M{"name": toolName, "arguments": args})
	var result M
	if callErr == nil {
		if json.Unmarshal(raw, &result) != nil || result["content"] == nil {
			callErr = errors.New("invalid tools/call response")
		}
	}
	if callErr != nil {
		h.mu.Lock()
		c.uncertain = "The last " + toolName + " call has an unknown outcome. Inspect the external app, then reconnect this server in L-Bridge before continuing."
		h.mu.Unlock()
		c.client.close()
		c.client = nil
		result = M{"isError": true, "content": []M{{"type": "text", "text": c.uncertain + " Transport error: " + callErr.Error()}}}
		s.mu.Lock()
		s.state.MCPWarnings[sid] = c.uncertain
		s.save()
		s.mu.Unlock()
	}
	rid := id()
	path := filepath.Join(s.dir, "mcp-results", gid)
	if err = os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(result)
	if err = atomicWrite(filepath.Join(path, rid+".json"), data); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _ = s.get(gid)
	receipt, _ := json.Marshal(M{"receipt_id": rid, "server_id": sid, "tool": toolName, "is_error": result["isError"], "preview": resultPreview(result)})
	g.add(agent, "mcp_result", string(receipt), cfg.Name+" · "+toolName+": "+resultPreview(result))
	if err = s.save(); err != nil {
		return nil, err
	}
	return M{"mcp_result": result, "receipt_id": rid}, nil
}
func resultPreview(result M) string {
	parts, _ := result["content"].([]any)
	for _, p := range parts {
		if m, ok := p.(map[string]any); ok && str(m, "type") == "text" {
			return clip(str(m, "text"), 350)
		}
	}
	if result["isError"] == true {
		return "Tool reported an error; inspect the shared receipt."
	}
	return "Result saved; both agents can read its shared receipt."
}
func (h *MCPHub) reset(sid string) error {
	c := h.connection(sid)
	select {
	case <-c.gate:
		defer func() { c.gate <- struct{}{} }()
		if c.client != nil {
			c.client.close()
			c.client = nil
		}
		h.mu.Lock()
		c.uncertain = ""
		c.goal = ""
		c.agent = ""
		c.lease = ""
		h.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("server %s is busy; pause its goal first", sid)
	}
}
