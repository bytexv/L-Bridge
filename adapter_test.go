package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdapterRetriesReadsButNotUncertainEdits(t *testing.T) {
	for _, name := range []string{"bridge_sync", "bridge_task"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			if err := writeEndpoint(dir, Endpoint{URL: server.URL, Token: "fixture-only"}); err != nil {
				t.Fatal(err)
			}
			data, err := adapterCall(context.Background(), dir, "claude", "fixture-only", M{"name": name, "arguments": M{"goal_id": "fixture"}})
			if name == "bridge_sync" {
				if err != nil || !json.Valid(data) || calls.Load() != 2 {
					t.Fatal("read did not recover", err, calls.Load())
				}
			} else if err == nil || calls.Load() != 1 {
				t.Fatal("uncertain mutation replayed", calls.Load())
			}
		})
	}
}
func TestAdapterRefreshesRejectedDesktopToken(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	var endpoint Endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.Header().Set("X-LBridge-Version", version)
			return
		}
		if calls.Add(1) == 1 {
			next := endpoint
			next.Token = "new-fixture-token"
			if err := writeEndpoint(dir, next); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"expired service token"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer new-fixture-token" {
			t.Error("stale token retained")
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	endpoint = Endpoint{URL: server.URL, Token: "old-fixture-token"}
	if err := writeEndpoint(dir, endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := adapterCall(context.Background(), dir, "claude", "", M{"name": "bridge_task", "arguments": M{}}); err != nil || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
}
func TestStartupWaitsForOtherAdapter(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-LBridge-Version", version) }))
	defer server.Close()
	finished := make(chan error, 1)
	finished <- errors.New("another process owns startup")
	written := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		written <- writeEndpoint(dir, Endpoint{URL: server.URL, Token: "fixture"})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e, err := waitForService(ctx, dir, finished)
	if err != nil || e.URL != server.URL {
		t.Fatal("reported failure before competing startup finished", err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}
