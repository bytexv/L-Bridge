package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, *Goal) {
	t.Helper()
	s, e := newStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	g, e := s.create("Test goal", "Make a tested result", "", "files", 120)
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range []string{"claude", "chatgpt"} {
		_, e = s.call(context.Background(), a, "bridge_join", M{"goal_id": g.ID, "role": "collaborator"})
		if e != nil {
			t.Fatal(e)
		}
	}
	return s, g
}
func call(t *testing.T, s *Store, g *Goal, agent, tool string, a M) any {
	t.Helper()
	if a == nil {
		a = M{}
	}
	a["goal_id"] = g.ID
	v, e := s.call(context.Background(), agent, tool, a)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestSharedDeltaAndDedup(t *testing.T) {
	s, g := fixture(t)
	after := len(g.Messages)
	args := M{"body": "compact work", "summary": "Readable summary", "nonce": "n1"}
	call(t, s, g, "claude", "bridge_send", args)
	call(t, s, g, "claude", "bridge_send", args)
	if len(g.Messages) != after+1 {
		t.Fatal("duplicate sent")
	}
	v := call(t, s, g, "chatgpt", "bridge_sync", M{"after": float64(after)}).(map[string]any)
	if len(v["messages"].([]any)) != 1 {
		t.Fatal("bad delta")
	}
	if v["cursor"].(float64) != float64(after+1) {
		t.Fatal("bad cursor")
	}
	if _, ok := v["original_goal"]; ok {
		t.Fatal("repeated full prompt")
	}
}
func TestTasksCannotBeDoubleClaimed(t *testing.T) {
	s, g := fixture(t)
	v := call(t, s, g, "claude", "bridge_task", M{"op": "create", "title": "Implement"})
	b, _ := json.Marshal(v)
	var task Task
	json.Unmarshal(b, &task)
	call(t, s, g, "claude", "bridge_task", M{"op": "claim", "task_id": task.ID})
	_, err := s.call(context.Background(), "chatgpt", "bridge_task", M{"goal_id": g.ID, "op": "claim", "task_id": task.ID})
	if err == nil {
		t.Fatal("double claim")
	}
	_, err = s.call(context.Background(), "chatgpt", "bridge_task", M{"goal_id": g.ID, "op": "done", "task_id": task.ID, "evidence": "fake"})
	if err == nil {
		t.Fatal("peer completed another task")
	}
}
func TestOptimisticFileWrites(t *testing.T) {
	s, g := fixture(t)
	call(t, s, g, "claude", "bridge_files", M{"op": "write", "path": "src/a.txt", "content": "first", "expected_hash": ""})
	_, err := s.call(context.Background(), "chatgpt", "bridge_files", M{"goal_id": g.ID, "op": "write", "path": "src/a.txt", "content": "lost update", "expected_hash": ""})
	if err == nil {
		t.Fatal("overwrote concurrent change")
	}
	call(t, s, g, "chatgpt", "bridge_files", M{"op": "write", "path": "src/a.txt", "content": "merged", "expected_hash": hash([]byte("first"))})
	b, _ := os.ReadFile(filepath.Join(g.Root, "src/a.txt"))
	if string(b) != "merged" {
		t.Fatal(string(b))
	}
}
func TestTraversalAndSymlinks(t *testing.T) {
	s, g := fixture(t)
	for _, p := range []string{"../outside", "/etc/passwd", "a/../../x", `C:\x`, "file:stream", "CON.txt", "a."} {
		_, e := s.call(context.Background(), "claude", "bridge_files", M{"goal_id": g.ID, "op": "write", "path": p, "content": "bad", "expected_hash": ""})
		if e == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if runtime.GOOS != "windows" {
		os.Symlink(t.TempDir(), filepath.Join(g.Root, "escape"))
		_, e := safePath(g.Root, "escape/a")
		if e == nil {
			t.Fatal("accepted symlink")
		}
	}
}
func TestPausedReadOnlyAndAllowance(t *testing.T) {
	s, g := fixture(t)
	g.Limit = 1
	call(t, s, g, "claude", "bridge_send", M{"body": "one", "summary": "one", "nonce": "one"})
	if g.Status != "paused" {
		t.Fatal("allowance did not pause")
	}
	_, e := s.call(context.Background(), "claude", "bridge_send", M{"goal_id": g.ID, "body": "two", "summary": "two", "nonce": "two"})
	if e == nil {
		t.Fatal("accepted after pause")
	}
	call(t, s, g, "claude", "bridge_files", M{"op": "list"})
	g.Status = "active"
	g.Limit = 100
	g.Mode = "read"
	_, e = s.call(context.Background(), "claude", "bridge_files", M{"goal_id": g.ID, "op": "write", "path": "a", "content": "a", "expected_hash": ""})
	if e == nil {
		t.Fatal("read-only write")
	}
}
func TestSummaryCompareAndSwap(t *testing.T) {
	s, g := fixture(t)
	call(t, s, g, "claude", "bridge_summary", M{"text": "first", "expected_version": float64(0)})
	_, e := s.call(context.Background(), "chatgpt", "bridge_summary", M{"goal_id": g.ID, "text": "stale", "expected_version": float64(0)})
	if e == nil {
		t.Fatal("stale summary accepted")
	}
}
func TestCompletionRequiresBothAndResets(t *testing.T) {
	s, g := fixture(t)
	call(t, s, g, "claude", "bridge_summary", M{"text": "Verified result", "expected_version": float64(0)})
	call(t, s, g, "claude", "bridge_finish", M{"evidence": "tested"})
	if g.Status == "completed" {
		t.Fatal("single vote completes")
	}
	call(t, s, g, "chatgpt", "bridge_send", M{"body": "correction", "summary": "correction", "nonce": "fix"})
	call(t, s, g, "chatgpt", "bridge_finish", M{"evidence": "reviewed fix"})
	if g.Status == "completed" {
		t.Fatal("stale vote accepted")
	}
	call(t, s, g, "claude", "bridge_finish", M{"evidence": "reviewed fix too"})
	if g.Status != "completed" {
		t.Fatal("did not complete")
	}
}
func TestRestartPersistsConversation(t *testing.T) {
	s, g := fixture(t)
	call(t, s, g, "claude", "bridge_send", M{"body": "日本語 works too", "summary": "Multilingual message", "nonce": "persist"})
	other, e := newStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	if other.state.Goals[0].Messages[len(g.Messages)-1].Body != "日本語 works too" {
		t.Fatal("missing after restart")
	}
}
func TestLongPollWakesOnPeer(t *testing.T) {
	s, g := fixture(t)
	after := len(g.Messages)
	done := make(chan error)
	go func() {
		_, e := s.call(context.Background(), "chatgpt", "bridge_sync", M{"goal_id": g.ID, "after": float64(after), "wait_seconds": float64(3)})
		done <- e
	}()
	time.Sleep(20 * time.Millisecond)
	call(t, s, g, "claude", "bridge_send", M{"body": "wake", "summary": "wake", "nonce": "wake"})
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("did not wake")
	}
}
func TestHTTPAuthOriginAndMethods(t *testing.T) {
	s, _ := fixture(t)
	app := &App{s: s, endpoint: Endpoint{URL: "http://127.0.0.1:48471", Token: "secret"}}
	cases := []struct {
		path, host, origin, auth, method string
		want                             int
	}{{"/api/state", "127.0.0.1:48471", "", "", "GET", 401}, {"/api/state", "evil.test", "", "Bearer secret", "GET", 403}, {"/api/state", "127.0.0.1:48471", "https://evil.test", "Bearer secret", "GET", 403}, {"/api/state", "127.0.0.1:48471", "", "Bearer secret", "GET", 200}, {"/api/shutdown", "127.0.0.1:48471", "", "Bearer secret", "GET", 400}}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://"+c.host+c.path, nil)
		r.Host = c.host
		r.Header.Set("Authorization", c.auth)
		r.Header.Set("Origin", c.origin)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Fatalf("%s: got %d want %d", c.path, w.Code, c.want)
		}
	}
}
func TestCommandPermissionAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	s, g := fixture(t)
	a := M{"goal_id": g.ID, "program": "/bin/sh", "args": []any{"-c", "printf started; sleep 20"}, "timeout_seconds": float64(1)}
	_, e := s.call(context.Background(), "claude", "bridge_run", a)
	if e == nil {
		t.Fatal("commands bypassed permission")
	}
	g.Mode = "commands"
	start := time.Now()
	v, e := s.call(context.Background(), "claude", "bridge_run", a)
	if e != nil {
		t.Fatal(e)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("command failed to stop")
	}
	if !v.(M)["timed_out_or_cancelled"].(bool) {
		t.Fatal("timeout not reported")
	}
}
func TestConcurrentMessagesAndSnapshots(t *testing.T) {
	s, g := fixture(t)
	app := &App{s: s, endpoint: Endpoint{URL: "http://127.0.0.1:48471", Token: "secret"}}
	var wg sync.WaitGroup
	for _, a := range []string{"claude", "chatgpt"} {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_, e := s.call(context.Background(), a, "bridge_send", M{"goal_id": g.ID, "body": "test", "summary": "test", "nonce": id()})
				if e != nil {
					t.Error(e)
				}
				r := httptest.NewRequest("GET", "http://127.0.0.1:48471/api/state", nil)
				r.Header.Set("Authorization", "Bearer secret")
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Error(w.Code)
				}
			}
		}(a)
	}
	wg.Wait()
	if len(g.Messages) != 23 {
		t.Fatal("lost messages", len(g.Messages))
	}
}
func TestCorruptStateIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("broken"), 0600)
	_, err := newStore(dir)
	if err == nil || !strings.Contains(err.Error(), "preserve") {
		t.Fatal("corruption ignored")
	}
}

func TestPauseCancelsRunningCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	s, g := fixture(t)
	g.Mode = "commands"
	done := make(chan error, 1)
	go func() {
		_, e := s.call(context.Background(), "claude", "bridge_run", M{"goal_id": g.ID, "program": "/bin/sh", "args": []any{"-c", "sleep 20"}, "timeout_seconds": float64(30)})
		done <- e
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		jobs.Lock()
		running := false
		for k := range jobs.items {
			if strings.HasPrefix(k, g.ID+":") {
				running = true
			}
		}
		jobs.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not register")
		}
		time.Sleep(5 * time.Millisecond)
	}
	app := &App{s: s}
	_, e := app.ui(httptest.NewRequest("POST", "http://localhost/api/control", nil), M{"goal_id": g.ID, "op": "pause"})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pause did not stop command")
	}
	if g.Status != "paused" {
		t.Fatal("pause state lost")
	}
}
