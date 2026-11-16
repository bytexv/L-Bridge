package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 0.2.x shares the adapter RPC contract. A patch update must not make an
// otherwise compatible running service look like a port conflict.
const minimumServiceVersion = "0.2.0"

var serviceStartup sync.Mutex

var errServiceLocked = errors.New("another L-Bridge service is starting or running for this data folder; reopen L-Bridge.exe after it finishes starting")
var errServiceRunning = errors.New("the recorded L-Bridge service is already running")

func holdServiceLock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Allow the previous process to release its handle during graceful restart.
	for i := 0; ; i++ {
		release, err := lockServiceFile(filepath.Join(dir, "service.lock"))
		if !errors.Is(err, errServiceLocked) || i == 3 {
			return release, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func listenForService(port int, automatic bool) (net.Listener, error) {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp4", address)
	if err == nil || !automatic || port == 0 {
		return listener, err
	}
	// Do not create a second workspace if an unrecorded legacy bridge owns
	// this port. Its data folder may be the same but its endpoint file missing.
	r, probeErr := localClient(time.Second).Get("http://" + address + "/api/health")
	if probeErr == nil {
		r.Body.Close()
		if _, versionErr := versionParts(r.Header.Get("X-LBridge-Version")); versionErr == nil {
			return nil, fmt.Errorf("an existing L-Bridge uses this port but could not be matched to this data folder; stop it through its Settings before restarting: %w", err)
		}
	}
	// The OS selects a free loopback port. Adapters discover it from the
	// authenticated endpoint record, so they do not need a fixed port number.
	return net.Listen("tcp4", "127.0.0.1:0")
}

// Only the authenticated service recorded in this data directory can be
// stopped. Never infer ownership from a port number or kill an arbitrary PID.
func validateEndpoint(e Endpoint) error {
	u, err := url.Parse(e.URL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid local endpoint")
	}
	host, port, err := net.SplitHostPort(u.Host)
	n, numberErr := strconv.Atoi(port)
	if err != nil || numberErr != nil || host != "127.0.0.1" || n < 1 || n > 65535 || e.Token == "" {
		return errors.New("invalid local endpoint")
	}
	return nil
}

func versionParts(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, errors.New("invalid bridge version")
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 100000 {
			return out, errors.New("invalid bridge version")
		}
		out[i] = n
	}
	return out, nil
}
func compareVersions(a, b string) int {
	x, _ := versionParts(a)
	y, _ := versionParts(b)
	for i := range x {
		if x[i] < y[i] {
			return -1
		}
		if x[i] > y[i] {
			return 1
		}
	}
	return 0
}
func localClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
func probeService(ctx context.Context, e Endpoint) (string, error) {
	if err := validateEndpoint(e); err != nil {
		return "", err
	}
	// New services expose a readiness check that never waits for the goal
	// store lock or serializes a potentially large conversation transcript.
	v, status, err := probePath(ctx, e, "/api/health")
	if status == http.StatusBadRequest || status == http.StatusNotFound {
		v, _, err = probePath(ctx, e, "/api/state") // Older releases.
	}
	return v, err
}
func probePath(ctx context.Context, e Endpoint, path string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", e.URL+path, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	r, err := localClient(time.Second).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer r.Body.Close()
	v := r.Header.Get("X-LBridge-Version")
	if r.StatusCode != http.StatusOK {
		return "", r.StatusCode, fmt.Errorf("existing service returned HTTP %d", r.StatusCode)
	}
	if _, err = versionParts(v); err != nil {
		return "", r.StatusCode, errors.New("the recorded endpoint did not identify itself as L-Bridge")
	}
	return v, r.StatusCode, nil
}
func alive(e Endpoint) bool {
	v, err := probeService(context.Background(), e)
	return err == nil && compareVersions(v, minimumServiceVersion) >= 0
}

func stopService(ctx context.Context, dir string, e Endpoint) error {
	if _, err := probeService(ctx, e); err != nil {
		return fmt.Errorf("could not verify the recorded L-Bridge service; no process was stopped: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", e.URL+"/api/shutdown", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Content-Type", "application/json")
	r, err := localClient(4 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("could not stop the existing bridge: %w", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("bridge refused shutdown (HTTP %d)", r.StatusCode)
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	address := strings.TrimPrefix(e.URL, "http://")
	for {
		// Old versions remove endpoint.json after cancelling their jobs. Wait
		// for that handoff as well as the listener closing before loading state.
		_, fileErr := os.Stat(endpointFile(dir))
		if os.IsNotExist(fileErr) {
			conn, dialErr := net.DialTimeout("tcp4", address, 100*time.Millisecond)
			if dialErr != nil {
				return nil
			}
			conn.Close()
		} else if current, err := loadEndpoint(dir); err == nil && (current.Token != e.Token || current.URL != e.URL) {
			return errors.New("another bridge started during restart; close connected hosts and try again")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("the old bridge has not finished stopping; close connected hosts and retry Restart L-Bridge.cmd")
		case <-tick.C:
		}
	}
}

// restart is an explicit launcher operation. Normal launches offer an upgrade
// only for older versions; a newer running version is reopened, never downgraded.
func existingService(dir string, restart bool, choose func(string) bool) (Endpoint, bool, error) {
	e, err := loadEndpoint(dir)
	if err != nil {
		return Endpoint{}, false, nil
	}
	running, err := probeService(context.Background(), e)
	if err != nil {
		return Endpoint{}, false, nil
	}
	if compareVersions(running, version) > 0 {
		return e, true, nil
	}
	if restart || (running != version && choose(running)) {
		if err = stopService(context.Background(), dir, e); err != nil {
			return Endpoint{}, false, err
		}
		return Endpoint{}, false, nil
	}
	return e, true, nil
}

func ensureServer(dir string) (Endpoint, error) {
	serviceStartup.Lock()
	defer serviceStartup.Unlock()
	if e, err := loadEndpoint(dir); err == nil {
		if running, err := probeService(context.Background(), e); err == nil {
			if compareVersions(running, minimumServiceVersion) >= 0 {
				return e, nil
			}
			return Endpoint{}, fmt.Errorf("L-Bridge %s is still running. Closing its browser tab does not stop it. Close the connected hosts and run Restart L-Bridge.cmd from the %s folder, then reopen the hosts", running, version)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return Endpoint{}, err
	}
	cmd := exec.Command(exe, "--serve", "--no-open", "--data-dir", dir)
	backgroundProcess(cmd)
	if err = cmd.Start(); err != nil {
		return Endpoint{}, err
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	for i := 0; i < 100; i++ {
		if e, err := loadEndpoint(dir); err == nil && alive(e) {
			return e, nil
		}
		select {
		case <-finished:
			// A simultaneous launch may have won the port and registered itself.
			if e, err := loadEndpoint(dir); err == nil && alive(e) {
				return e, nil
			}
			return Endpoint{}, errors.New("local bridge could not start; open L-Bridge.exe or use Restart L-Bridge.cmd. Closing a browser tab does not stop the service")
		case <-time.After(100 * time.Millisecond):
		}
	}
	return Endpoint{}, errors.New("local bridge did not start; open L-Bridge.exe to inspect the error")
}

func stopRecordedService(dir string) error {
	e, err := loadEndpoint(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// An existing but unreachable record is not permission to kill anything.
	return stopService(context.Background(), dir, e)
}

func writeEndpoint(dir string, e Endpoint) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "endpoint.json"), b)
}
