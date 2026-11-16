package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMCPChild(t *testing.T) {
	if os.Getenv("LBRIDGE_MCP_TEST_CHILD") != "yes" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var r rpcRequest
		json.Unmarshal(sc.Bytes(), &r)
		if len(r.ID) == 0 {
			continue
		}
		var result any = M{}
		switch r.Method {
		case "initialize":
			result = M{"protocolVersion": "2025-06-18", "capabilities": M{"tools": M{}}, "serverInfo": M{"name": "test", "version": "1"}}
		case "tools/list":
			if str(r.Params, "cursor") == "" {
				result = M{"tools": []M{{"name": "echo", "inputSchema": M{"type": "object"}}}, "nextCursor": "2"}
			} else {
				result = M{"tools": []M{{"name": "slow", "inputSchema": M{"type": "object"}}, {"name": "disconnect", "inputSchema": M{"type": "object"}}}}
			}
		case "tools/call":
			switch str(r.Params, "name") {
			case "slow":
				time.Sleep(10 * time.Second)
			case "disconnect":
				os.Exit(0)
			}
			result = M{"content": []M{{"type": "text", "text": "shared scene result"}, {"type": "image", "mimeType": "image/png", "data": "dGVzdA=="}}, "isError": false}
		}
		json.NewEncoder(os.Stdout).Encode(M{"jsonrpc": "2.0", "id": r.ID, "result": result})
	}
	os.Exit(0)
}
func mcpFixture(t *testing.T) (*Store, *Goal) {
	t.Helper()
	t.Setenv("LBRIDGE_MCP_TEST_CHILD", "yes")
	s, g := fixture(t)
	g.Mode = "commands"
	g.MCPServers = []string{"studio"}
	s.state.Settings.Servers = []ServerConfig{{ID: "studio", Name: "Test Studio", Command: os.Args[0], Args: []string{"-test.run=^TestMCPChild$"}}}
	t.Cleanup(func() { s.runtime.close() })
	return s, g
}
func lease(t *testing.T, s *Store, g *Goal, agent string) string {
	t.Helper()
	out := call(t, s, g, agent, "bridge_mcp", M{"op": "acquire", "server_id": "studio"}).(M)
	if out["acquired"] != true {
		t.Fatalf("lease unavailable: %v", out)
	}
	return str(out, "lease")
}
func TestMCPSharedReceiptAndExclusiveLease(t *testing.T) {
	s, g := mcpFixture(t)
	tools := call(t, s, g, "claude", "bridge_mcp", M{"op": "tools", "server_id": "studio"}).(M)
	if len(tools["tools"].([]M)) != 3 {
		t.Fatal("pagination incomplete")
	}
	l := lease(t, s, g, "claude")
	other := call(t, s, g, "chatgpt", "bridge_mcp", M{"op": "acquire", "server_id": "studio"}).(M)
	if other["acquired"] != false {
		t.Fatal("peer acquired active lease")
	}
	_, err := s.call(context.Background(), "chatgpt", "bridge_mcp", M{"goal_id": g.ID, "op": "call", "server_id": "studio", "tool": "echo", "lease": l, "arguments": M{}})
	if err == nil {
		t.Fatal("peer bypassed owner")
	}
	out := call(t, s, g, "claude", "bridge_mcp", M{"op": "call", "server_id": "studio", "tool": "echo", "lease": l, "arguments": M{}}).(M)
	rid := str(out, "receipt_id")
	peer := call(t, s, g, "chatgpt", "bridge_mcp", M{"op": "result", "receipt_id": rid}).(M)
	b, _ := json.Marshal(peer)
	if !strings.Contains(string(b), "image/png") || !strings.Contains(string(b), "shared scene result") {
		t.Fatal("shared result lost content", string(b))
	}
	call(t, s, g, "claude", "bridge_mcp", M{"op": "release", "server_id": "studio", "lease": l})
	lease(t, s, g, "chatgpt")
}
func TestMCPExpiredLeaseAndGoalPermission(t *testing.T) {
	s, g := mcpFixture(t)
	l := lease(t, s, g, "claude")
	h := s.runtime.hub
	c := h.connection("studio")
	h.mu.Lock()
	c.expires = time.Now().Add(-time.Second)
	h.mu.Unlock()
	args := M{"goal_id": g.ID, "op": "call", "server_id": "studio", "tool": "echo", "lease": l, "arguments": M{}}
	if _, e := s.call(context.Background(), "claude", "bridge_mcp", args); e == nil {
		t.Fatal("expired lease executed")
	}
	fresh := lease(t, s, g, "claude")
	if fresh == l {
		t.Fatal("expired lease was reused")
	}
	if _, e := s.call(context.Background(), "claude", "bridge_mcp", args); e == nil {
		t.Fatal("stale queued call reused a renewed lease")
	}
	g.MCPServers = nil
	if _, e := s.call(context.Background(), "claude", "bridge_mcp", M{"goal_id": g.ID, "op": "acquire", "server_id": "studio"}); e == nil {
		t.Fatal("unapproved server accessible")
	}
}
func TestMCPAmbiguousResultPersistsUntilReview(t *testing.T) {
	s, g := mcpFixture(t)
	l := lease(t, s, g, "claude")
	out := call(t, s, g, "claude", "bridge_mcp", M{"op": "call", "server_id": "studio", "tool": "disconnect", "lease": l, "arguments": M{}}).(M)
	if out["mcp_result"].(M)["isError"] != true {
		t.Fatal("disconnect reported success")
	}
	s2, err := newStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.runtime.close()
	if _, err = s2.runtime.hub.acquire("studio", g.ID, "chatgpt"); err == nil {
		t.Fatal("uncertain outcome lost across restart")
	}
	app := &App{s: s2}
	if _, err = app.runtimeUI(httptest.NewRequest("POST", "http://local/api/mcp/reset", nil), M{"server_id": "studio"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s2.runtime.hub.acquire("studio", g.ID, "chatgpt"); err != nil {
		t.Fatal(err)
	}
}
func TestMCPPauseCancelsAndRejectsQueuedCall(t *testing.T) {
	s, g := mcpFixture(t)
	l := lease(t, s, g, "claude")
	done := make(chan error, 2)
	args := func(tool string) M {
		return M{"goal_id": g.ID, "op": "call", "server_id": "studio", "tool": tool, "lease": l, "arguments": M{}}
	}
	go func() { _, e := s.call(context.Background(), "claude", "bridge_mcp", args("slow")); done <- e }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		actions := g.Actions
		s.mu.Unlock()
		if actions > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("MCP call did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	go func() { _, e := s.call(context.Background(), "claude", "bridge_mcp", args("echo")); done <- e }()
	app := &App{s: s}
	if _, err := app.ui(httptest.NewRequest("POST", "http://local/api/control", nil), M{"goal_id": g.ID, "op": "pause"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("MCP cancellation hung")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Actions != 1 {
		t.Fatal("queued edit executed after pause", g.Actions)
	}
}
func TestRunnerScopeCannotChangeSettingsOrOtherGoals(t *testing.T) {
	s, g := fixture(t)
	s.runtime.tokens["scoped"] = RunnerScope{g.ID, "claude"}
	app := &App{s: s, endpoint: Endpoint{URL: "http://127.0.0.1:48471", Token: "master"}}
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/state", "", 401},
		{"/api/runner/settings", `{}`, 401},
		{"/rpc", `{"agent":"chatgpt","name":"bridge_join","arguments":{"goal_id":"` + g.ID + `","role":"x"}}`, 400},
		{"/rpc", `{"agent":"claude","name":"bridge_join","arguments":{"goal_id":"other","role":"x"}}`, 400},
		{"/rpc", `{"agent":"claude","name":"bridge_join","arguments":{"goal_id":"` + g.ID + `","role":"x"}}`, 200},
	} {
		r := httptest.NewRequest(http.MethodPost, app.endpoint.URL+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer scoped")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s got %d: %s", tc.path, w.Code, w.Body.String())
		}
	}
	s.runtime.stop(g.ID)
	if _, ok := s.runtime.scope("scoped"); ok {
		t.Fatal("stop did not revoke runner token")
	}
}
func TestRunnerStructuredEvents(t *testing.T) {
	c := &runnerStream{agent: "claude"}
	c.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"private"},{"type":"text","text":"Working"}]}}
{"type":"result","subtype":"success","result":"Done","is_error":false,"usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":3,"cache_read_input_tokens":7}}
`))
	if !c.completed || c.final != "Done" || c.input != 20 || c.output != 20 || c.cached != 7 {
		t.Fatalf("Claude parsing: %+v", c)
	}
	o := &runnerStream{agent: "chatgpt"}
	data := `{"type":"item.completed","item":{"type":"agent_message","text":"Built"}}
{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":20,"cached_input_tokens":50}}
`
	for _, b := range []byte(data) {
		o.Write([]byte{b})
	}
	if !o.completed || o.input != 100 || o.cached != 50 || o.final != "Built" {
		t.Fatalf("Codex parsing: %+v", o)
	}
	f := &runnerStream{agent: "claude"}
	f.Write([]byte(`{"type":"result","subtype":"error_max_turns","is_error":true}`))
	f.flush()
	if f.completed || f.failure == "" {
		t.Fatal("provider failure swallowed")
	}
}
func TestRunnerArgsAndPersistence(t *testing.T) {
	cfg := Settings{}
	cfg.defaults()
	dir := t.TempDir()
	for _, agent := range []string{"claude", "chatgpt"} {
		args, err := runnerArgs(agent, cfg, dir, `C:\Users\Some Person\LBridge`, `C:\L Bridge\L-Bridge.exe`, "test-token")
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(args, "\n")
		if strings.Contains(text, "bypass") || strings.Contains(text, "danger-full") {
			t.Fatal("unrestricted runner")
		}
		if agent == "claude" {
			b, e := os.ReadFile(filepath.Join(dir, "mcp.json"))
			if e != nil || !json.Valid(b) {
				t.Fatal("bad MCP config")
			}
		} else if !strings.Contains(text, "read-only") || !strings.Contains(text, "--ignore-user-config") {
			t.Fatal("missing runner isolation")
		}
	}
	s, g := fixture(t)
	g.Team = &TeamRun{Status: "running", Agents: map[string]*RunnerState{}}
	s.save()
	restored, err := newStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.state.Goals[0].Team.Status != "interrupted" || restored.state.Goals[0].Status != "paused" {
		t.Fatal("run auto-resumed after crash")
	}
}
func TestMCPConcurrentLeaseClaims(t *testing.T) {
	h := newMCPHub()
	var wg sync.WaitGroup
	wins := make(chan string, 2)
	for _, a := range []string{"claude", "chatgpt"} {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			out, err := h.acquire("same", "goal", a)
			if err == nil && out.(M)["acquired"] == true {
				wins <- a
			}
		}(a)
	}
	wg.Wait()
	close(wins)
	if len(wins) != 1 {
		t.Fatal("expected exactly one winner", len(wins))
	}
}

func TestRelativeDataDirectoryIsCanonicalForChildren(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s, err := newStore("data")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(s.dir) || s.dir != filepath.Join(cwd, "data") {
		t.Fatal("runner data path must remain valid from its temporary working directory", s.dir)
	}
}

func TestWebAccessRunnerPolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := Settings{WebAccess: enabled}
		cfg.defaults()
		for _, agent := range []string{"claude", "chatgpt"} {
			args, err := runnerArgs(agent, cfg, t.TempDir(), t.TempDir(), "bridge", "fixture-token")
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, "\n")
			if agent == "claude" {
				if strings.Contains(joined, "WebSearch,WebFetch") != enabled {
					t.Fatal("Claude web policy mismatch")
				}
				for i, a := range args {
					if a == "--tools" && !enabled && args[i+1] != "" {
						t.Fatal("unexpected native tools")
					}
				}
			} else if strings.Contains(joined, `web_search="live"`) != enabled {
				t.Fatal("Codex web policy mismatch")
			}
		}
	}
	s, g := fixture(t)
	g.WebAccess = true
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	restored, err := newStore(s.dir)
	if err != nil || !restored.state.Goals[0].WebAccess {
		t.Fatal("lost web preference", err)
	}
}

func TestMCPBlockedWriteHonorsDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	c := &MCPClient{in: writer, cancel: func() {}, pending: map[string]chan rpcReply{}, done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.call(ctx, "tools/call", M{"name": "fixture"}); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked transport ignored deadline")
	}
}
