package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runningService(t *testing.T, v string) (string, Endpoint, *atomic.Int32) {
	t.Helper()
	dir := t.TempDir()
	token := id()
	var stops atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-LBridge-Version", v)
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/api/state" {
			w.Write([]byte("{}"))
			return
		}
		if r.URL.Path == "/api/shutdown" && r.Method == "POST" {
			stops.Add(1)
			w.Write([]byte("{}"))
			go func() { time.Sleep(20 * time.Millisecond); server.Close(); os.Remove(endpointFile(dir)) }()
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	e := Endpoint{URL: server.URL, Token: token, PID: os.Getpid()}
	if err := writeEndpoint(dir, e); err != nil {
		t.Fatal(err)
	}
	return dir, e, &stops
}

func TestLaunchReusesRunningService(t *testing.T) {
	dir, e, stops := runningService(t, version)
	got, found, err := existingService(dir, false, func(string) bool { t.Fatal("same version requested upgrade"); return false })
	if err != nil || !found || got != e || stops.Load() != 0 {
		t.Fatal(got, found, err)
	}
}
func TestOlderServiceCanBeReopenedWithoutStopping(t *testing.T) {
	dir, e, stops := runningService(t, "0.1.0")
	asked := false
	got, found, err := existingService(dir, false, func(v string) bool { asked = true; return false })
	if err != nil || !found || got != e || !asked || stops.Load() != 0 {
		t.Fatal("reopen failed", err)
	}
}
func TestUpgradeStopsVerifiedOlderServiceAndKeepsFiles(t *testing.T) {
	dir, _, stops := runningService(t, "0.1.0")
	path := filepath.Join(dir, "state.json")
	saved := []byte("saved goal fixture")
	if err := os.WriteFile(path, saved, 0600); err != nil {
		t.Fatal(err)
	}
	_, found, err := existingService(dir, false, func(string) bool { return true })
	if err != nil || found || stops.Load() != 1 {
		t.Fatal("upgrade failed", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != string(saved) {
		t.Fatal("saved state changed")
	}
}
func TestRestartCurrentServiceAndNeverDowngradeNewer(t *testing.T) {
	dir, _, stops := runningService(t, version)
	_, found, err := existingService(dir, true, func(string) bool { t.Fatal("explicit restart prompted"); return false })
	if err != nil || found || stops.Load() != 1 {
		t.Fatal("restart failed", err)
	}
	dir, _, stops = runningService(t, "0.5.0")
	_, found, err = existingService(dir, true, func(string) bool { t.Fatal("newer version requested downgrade"); return false })
	if err != nil || !found || stops.Load() != 0 {
		t.Fatal("newer service was stopped", err)
	}
}
func TestAdapterReportsOlderServiceWithoutSpawning(t *testing.T) {
	dir, _, stops := runningService(t, "0.1.0")
	_, err := ensureServer(dir)
	if err == nil || !strings.Contains(err.Error(), "Restart L-Bridge.cmd") || stops.Load() != 0 {
		t.Fatal(err)
	}
}
func TestAdapterReusesCompatiblePatchService(t *testing.T) {
	dir, e, stops := runningService(t, "0.2.0")
	got, err := ensureServer(dir)
	if err != nil || got != e || stops.Load() != 0 {
		t.Fatal("compatible service not reused", err)
	}
}

func TestHealthProbeDoesNotWaitForStore(t *testing.T) {
	s := &Store{}
	app := &App{s: s, endpoint: Endpoint{Token: "health-secret"}}
	server := httptest.NewServer(app)
	defer server.Close()
	app.endpoint.URL = server.URL
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := probeService(context.Background(), app.endpoint)
	if err != nil || v != version {
		t.Fatal("readiness blocked by busy store", err)
	}
}
func TestEndpointValidationAndRedirectProtection(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:1234@evil.test", "http://127.0.0.1:1234/path", "http://localhost:1234", "http://127.0.0.1:0", "https://127.0.0.1:1234", "http://127.0.0.1:1234?redirect=evil"} {
		if validateEndpoint(Endpoint{URL: raw, Token: "secret"}) == nil {
			t.Fatal("unsafe endpoint accepted", raw)
		}
	}
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.Header().Set("X-LBridge-Version", version)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	if _, err := probeService(context.Background(), Endpoint{URL: redirect.URL, Token: "secret"}); err == nil {
		t.Fatal("redirect accepted")
	}
	if targetHits.Load() != 0 {
		t.Fatal("followed redirect")
	}
}
func TestStopRefusesUnverifiedProcess(t *testing.T) {
	var stops atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/shutdown" {
			stops.Add(1)
		}
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	err := stopService(context.Background(), t.TempDir(), Endpoint{URL: server.URL, Token: "secret"})
	if err == nil || stops.Load() != 0 {
		t.Fatal("unverified application was stopped")
	}
}
func TestOccupiedPortDoesNotAlterStoredState(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	os.WriteFile(path, []byte("preserve"), 0600)
	err = runServer(dir, listener.Addr().(*net.TCPAddr).Port, false)
	if !errors.Is(err, errPortInUse) || !strings.Contains(err.Error(), "Closing the browser tab") {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "preserve" {
		t.Fatal("occupied port changed state")
	}
}

func TestServiceLockRejectsSecondOwnerAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.lock")
	release, err := lockServiceFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	if other, err := lockServiceFile(path); !errors.Is(err, errServiceLocked) {
		if other != nil {
			other()
		}
		t.Fatal("second service was allowed", err)
	}
	release()
	release = nil
	next, err := lockServiceFile(path)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	next()
}

func TestPortFallbackLeavesOtherApplicationRunning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("other app")) }))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	listener, err := listenForService(port, true)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if listener.Addr().(*net.TCPAddr).Port == port {
		t.Fatal("reused occupied port")
	}
	r, err := http.Get(server.URL)
	if err != nil {
		t.Fatal("other app stopped", err)
	}
	r.Body.Close()
	if explicit, err := listenForService(port, false); err == nil {
		explicit.Close()
		t.Fatal("explicit port silently changed")
	}
}

func TestPortFallbackRefusesUnrecordedLegacyBridge(t *testing.T) {
	_, e, stops := runningService(t, "0.1.0")
	u, _ := net.ResolveTCPAddr("tcp4", strings.TrimPrefix(e.URL, "http://"))
	if listener, err := listenForService(u.Port, true); err == nil {
		listener.Close()
		t.Fatalf("started beside unrecorded legacy bridge: recorded=%s port=%d", e.URL, u.Port)
	}
	if stops.Load() != 0 {
		t.Fatal("stopped unverified bridge")
	}
}
