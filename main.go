package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed web/*
var web embed.FS

type Endpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func dataDir() string {
	if p := os.Getenv("LBRIDGE_HOME"); p != "" {
		return p
	}
	d, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(d, "L-Bridge")
}
func endpointFile(dir string) string { return filepath.Join(dir, "endpoint.json") }
func loadEndpoint(dir string) (Endpoint, error) {
	var e Endpoint
	b, err := os.ReadFile(endpointFile(dir))
	if err != nil {
		return e, err
	}
	err = json.Unmarshal(b, &e)
	if err != nil {
		return e, err
	}
	return e, validateEndpoint(e)
}
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
func main() {
	mcp := flag.Bool("mcp", false, "Run the MCP stdio adapter")
	agent := flag.String("agent", "chatgpt", "Host identity: chatgpt or claude")
	serve := flag.Bool("serve", false, "Run local bridge service")
	noOpen := flag.Bool("no-open", false, "Do not open a browser")
	restart := flag.Bool("restart", false, "Gracefully stop the recorded bridge and launch this version")
	update := flag.Bool("update", false, "Check and install the latest Windows GitHub release")
	stopOnly := flag.Bool("stop", false, "Gracefully stop the recorded local bridge")
	dir := flag.String("data-dir", dataDir(), "Local state directory")
	port := flag.Int("port", 48471, "Loopback service port")
	flag.Parse()
	automaticPort := true
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			automaticPort = false
		}
	})
	absolute, err := filepath.Abs(*dir)
	if err != nil {
		showStartupError(err.Error())
		os.Exit(1)
	}
	*dir = absolute
	if *update {
		if err := launchUpdate(*dir); err != nil {
			showStartupError(err.Error())
			os.Exit(1)
		}
		return
	}
	if *stopOnly {
		if err := stopRecordedService(*dir); err != nil {
			showStartupError(err.Error())
			os.Exit(1)
		}
		return
	}
	if *mcp {
		if *agent != "claude" && *agent != "chatgpt" {
			log.Fatal("agent must be claude or chatgpt")
		}
		stdio(*dir, *agent)
		return
	}
	choose := func(running string) bool {
		if *serve || *noOpen {
			return false
		}
		return offerRestart(running)
	}
	e, found, err := existingService(*dir, *restart, choose)
	if err != nil {
		showStartupError(err.Error())
		os.Exit(1)
	}
	if found {
		if !*noOpen {
			openBrowser(e.URL + "/#token=" + e.Token)
		}
		return
	}
	if err := runLocalServer(*dir, *port, !*noOpen && !*serve, automaticPort); err != nil {
		// Another launch can bind between the initial probe and Listen.
		if errors.Is(err, errPortInUse) || errors.Is(err, errServiceLocked) || errors.Is(err, errServiceRunning) {
			for i := 0; i < 40; i++ {
				if existing, er := loadEndpoint(*dir); er == nil && alive(existing) {
					if !*noOpen && !*serve {
						openBrowser(existing.URL + "/#token=" + existing.Token)
					}
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		showStartupError(err.Error())
		os.Exit(1)
	}
}

var errPortInUse = errors.New("bridge port unavailable")

func runServer(dir string, port int, show bool) error {
	return runLocalServer(dir, port, show, false)
}
func runLocalServer(dir string, port int, show, automaticPort bool) error {
	release, err := holdServiceLock(dir)
	if err != nil {
		return err
	}
	defer release()
	// Legacy services do not take the new lock. A reachable recorded endpoint
	// is still authoritative; never open a second store alongside that process.
	if recorded, er := loadEndpoint(dir); er == nil {
		if _, er = probeService(context.Background(), recorded); er == nil {
			return errServiceRunning
		}
		if conn, dialErr := net.DialTimeout("tcp4", strings.TrimPrefix(recorded.URL, "http://"), 200*time.Millisecond); dialErr == nil {
			conn.Close()
			return errors.New("the recorded bridge port is reachable but could not be authenticated; no second service was started. Reopen the original bridge or use Settings > Stop local bridge, then update both plugin packages")
		}
	}
	listener, err := listenForService(port, automaticPort)
	if err != nil {
		return fmt.Errorf("%w: cannot listen on 127.0.0.1:%d.\n\nClosing the browser tab does not stop L-Bridge. Close connected hosts, then use Restart L-Bridge.cmd. You can also reopen the running bridge and choose Settings > Stop local bridge.\n\nIf another application owns this port, leave it running and start L-Bridge with --port followed by an unused port number.\n\nDetails: %v", errPortInUse, port, err)
	}
	defer listener.Close()
	s, err := newStore(dir)
	if err != nil {
		return err
	}
	defer s.runtime.close()
	e := Endpoint{"http://" + listener.Addr().String(), id() + id(), os.Getpid()}
	if err = writeEndpoint(dir, e); err != nil {
		return err
	}
	app := &App{s: s, endpoint: e}
	server := http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 150 * time.Second, IdleTimeout: 60 * time.Second}
	app.shutdown = func() {
		s.runtime.close()
		go func() {
			time.Sleep(150 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			server.Shutdown(ctx)
		}()
	}
	fmt.Fprintln(os.Stderr, "L-Bridge", version, "listening on", e.URL)
	if show {
		openBrowser(e.URL + "/#token=" + e.Token)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); app.shutdown() }()
	err = server.Serve(listener)
	jobs.Lock()
	for _, cancel := range jobs.items {
		cancel()
	}
	jobs.Unlock()
	if current, er := loadEndpoint(dir); er == nil && current.Token == e.Token {
		os.Remove(endpointFile(dir))
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type App struct {
	s        *Store
	endpoint Endpoint
	shutdown func()
}

func (app *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-LBridge-Version", version)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if r.Host != strings.TrimPrefix(app.endpoint.URL, "http://") {
		http.Error(w, "Invalid host", 403)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != app.endpoint.URL {
		http.Error(w, "Invalid origin", 403)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/rpc" {
		master := subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+app.endpoint.Token)) == 1
		var scope RunnerScope
		scoped := false
		if !master && app.s.runtime != nil {
			scope, scoped = app.s.runtime.scope(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		}
		if !master && (!scoped || r.URL.Path != "/rpc" || r.Method != "POST") {
			http.Error(w, "Open L-Bridge from the executable to connect this browser.", 401)
			return
		}
		if r.Method != "GET" && r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/health" && r.Method == "GET" {
			json.NewEncoder(w).Encode(M{"version": version, "ready": true})
			return
		}
		var a M
		if r.Method == "POST" {
			r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
			defer r.Body.Close()
			if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
				apiError(w, errors.New("invalid JSON"))
				return
			}
		}
		if r.URL.Path == "/rpc" && r.Method == "POST" {
			params, _ := a["arguments"].(map[string]any)
			if scoped {
				if str(a, "agent") != scope.Agent || str(params, "goal_id") != scope.Goal || str(a, "name") == "bridge_create" || str(a, "name") == "bridge_goals" {
					apiError(w, errors.New("runner tools are restricted to the assigned goal"))
					return
				}
			}
			result, err := app.s.call(r.Context(), str(a, "agent"), str(a, "name"), params)
			if err != nil {
				apiError(w, err)
			} else {
				json.NewEncoder(w).Encode(result)
			}
			return
		}
		result, err := app.ui(r, a)
		if err != nil {
			apiError(w, err)
			return
		}
		json.NewEncoder(w).Encode(result)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	files, _ := fs.Sub(web, "web")
	http.FileServer(http.FS(files)).ServeHTTP(w, r)
}
func apiError(w http.ResponseWriter, err error) {
	w.WriteHeader(400)
	json.NewEncoder(w).Encode(M{"error": err.Error()})
}
func (app *App) ui(r *http.Request, a M) (any, error) {
	s := app.s
	if strings.HasPrefix(r.URL.Path, "/api/team/") || strings.HasPrefix(r.URL.Path, "/api/runner/") || strings.HasPrefix(r.URL.Path, "/api/mcp/") {
		if r.Method != "POST" {
			return nil, errors.New("POST required")
		}
		return app.runtimeUI(r, a)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/api/state":
		if r.Method != "GET" {
			break
		}
		b, _ := json.Marshal(M{"busy": s.runtime.busy() || s.runtime.hub.busy() || activeCommands(), "version": version, "goals": s.state.Goals, "data_dir": s.dir, "platform": runtime.GOOS, "executable": executablePath(), "settings": s.state.Settings, "mcp_status": s.runtime.hub.snapshot()})
		return json.RawMessage(b), nil
	case "/api/create":
		if r.Method != "POST" {
			break
		}
		g, err := s.create(str(a, "title"), str(a, "prompt"), str(a, "root"), str(a, "mode"), num(a, "limit", 120))
		if err != nil {
			return nil, err
		}
		g.WebAccess = a["web_access"] == true
		return M{"id": g.ID}, s.save()
	case "/api/demo":
		if r.Method != "POST" {
			break
		}
		for _, g := range s.state.Goals {
			if g.Demo {
				return M{"id": g.ID}, nil
			}
		}
		g, err := s.create("A tiny studio. A big launch.", "Design a useful launch plan for an independent creative studio. Split the work, challenge assumptions, and deliver a clear plan.", "", "files", 120)
		if err != nil {
			return nil, err
		}
		g.Demo = true
		g.Status = "demo"
		g.Agents = map[string]Agent{"claude": {"Strategy & research", now()}, "chatgpt": {"Product & delivery", now()}}
		g.add("claude", "decision", `{"role":"strategy","next":"audience+positioning","ask":"take launch plan?"}`, "I’ll shape the audience and positioning. ChatGPT, take the launch plan?")
		g.add("chatgpt", "work", `{"accept":"launch","need":"audience","plan":["offer","page","outreach"]}`, "On it. I’ll map a focused launch around one offer, one page, and a short outreach list.")
		g.add("claude", "review", `{"risk":"too broad","fix":"one audience, one offer"}`, "Let’s narrow the offer before we build. A smaller launch will be easier to measure.")
		g.Summary = "The team is shaping a focused studio launch. Claude owns positioning; ChatGPT owns delivery. They agreed to start with one audience and one offer. Next: validate the offer, then finish the launch checklist.\n\nThis is a scripted preview. No models are connected and no work has been executed."
		g.SummaryBy = "demo"
		g.SummaryVersion = 1
		g.Tasks = []Task{{ID: "demo1", Title: "Define the studio’s first audience", Owner: "claude", Status: "done", Evidence: "Scripted example"}, {ID: "demo2", Title: "Map the launch checklist", Owner: "chatgpt", Status: "doing"}, {ID: "demo3", Title: "Cross-check the plan", Owner: "", Status: "todo"}}
		g.Actions = 7
		g.BytesSent = 286
		return M{"id": g.ID}, s.save()
	case "/api/control":
		if r.Method != "POST" {
			break
		}
		g, err := s.get(str(a, "goal_id"))
		if err != nil {
			return nil, err
		}
		if g.Demo {
			return nil, errors.New("demo is read-only; create a live goal")
		}
		switch str(a, "op") {
		case "pause":
			g.Status = "paused"
			cancelGoal(g.ID)
			s.runtime.stop(g.ID)
		case "resume":
			if g.Actions >= g.Limit {
				return nil, errors.New("increase the action allowance before resuming")
			}
			g.Status = "active"
			g.Votes = map[string]string{}
		case "settings":
			limit := num(a, "limit", g.Limit)
			mode := str(a, "mode")
			if limit < 10 || limit > 10000 {
				return nil, errors.New("allowance must be 10–10000")
			}
			if mode != "read" && mode != "files" && mode != "commands" {
				return nil, errors.New("invalid access mode")
			}
			if value, ok := a["web_access"].(bool); ok {
				if value != g.WebAccess {
					s.runtime.stop(g.ID)
				}
				g.WebAccess = value
			}
			g.Limit = limit
			g.Mode = mode
			if mode != "commands" {
				cancelGoal(g.ID)
				s.runtime.stop(g.ID)
			}
		default:
			return nil, errors.New("unknown control")
		}
		g.add("you", "status", "Workspace control: "+str(a, "op"), "You updated the workspace: "+str(a, "op"))
		return M{"status": g.Status}, s.save()
	case "/api/message":
		if r.Method != "POST" {
			break
		}
		g, err := s.get(str(a, "goal_id"))
		if err != nil {
			return nil, err
		}
		if g.Demo {
			return nil, errors.New("create a live goal to send a message")
		}
		text := strings.TrimSpace(str(a, "body"))
		if text == "" || len(text) > 8000 {
			return nil, errors.New("message must be 1–8000 bytes")
		}
		g.add("you", "direction", text, text)
		g.Votes = map[string]string{}
		if g.Status == "completed" {
			g.Status = "paused"
		}
		return M{"ok": true}, s.save()
	case "/api/export":
		if r.Method != "GET" {
			break
		}
		g, err := s.get(r.URL.Query().Get("goal"))
		if err != nil {
			return nil, err
		}
		b, _ := json.MarshalIndent(g, "", "  ")
		return json.RawMessage(b), nil
	case "/api/open-folder":
		if r.Method != "POST" {
			break
		}
		g, err := s.get(str(a, "goal_id"))
		if err != nil {
			return nil, err
		}
		return M{"ok": true}, openBrowser(g.Root)
	case "/api/shutdown":
		if r.Method != "POST" {
			break
		}
		app.shutdown()
		return M{"ok": true}, nil
	}
	return nil, errors.New("unknown route or method")
}
func executablePath() string { p, _ := os.Executable(); return p }
