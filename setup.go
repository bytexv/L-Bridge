package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unicode/utf16"
)

func psLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func setupScript(agent, action, executable string, prefix []string) (string, error) {
	if agent != "claude" && agent != "chatgpt" {
		return "", errors.New("choose Claude or Codex")
	}
	var body string
	switch action {
	case "install":
		if agent == "claude" {
			body = "Invoke-RestMethod 'https://claude.ai/install.ps1' | Invoke-Expression"
		} else {
			body = `if (-not (Get-Command npm.cmd -ErrorAction SilentlyContinue)) { Start-Process 'https://nodejs.org/en/download'; throw 'Install Node.js LTS from the opened page, then click Install Codex again.' }; & npm.cmd install -g @openai/codex; if ($LASTEXITCODE -ne 0) { throw 'Codex installation failed. See the output above.' }`
		}
	case "login":
		if executable == "" {
			return "", errors.New("install this agent first, then click Sign in")
		}
		args := append([]string{}, prefix...)
		if agent == "claude" {
			args = append(args, "auth", "login")
		} else {
			args = append(args, "login")
		}
		body = "& " + psLiteral(executable)
		for _, a := range args {
			body += " " + psLiteral(a)
		}
		body += "; if ($LASTEXITCODE -ne 0) { throw 'Sign-in did not finish. See the output above.' }"
	default:
		return "", errors.New("unknown setup action")
	}
	// Refresh PATH in this child only, so newly installed programs are discoverable.
	return `$ErrorActionPreference='Stop'; $env:Path=[Environment]::GetEnvironmentVariable('Path','Machine')+';'+[Environment]::GetEnvironmentVariable('Path','User'); try { ` + body + `; Write-Host 'Return to L-Bridge and click Check connections.' } catch { Write-Host $_ -ForegroundColor Red }; Read-Host 'Press Enter to close'`, nil
}
func (app *App) setupAgent(a M) (any, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("the guided installer and sign-in window are for Windows; use the linked official guide on this platform")
	}
	agent, action := str(a, "agent"), str(a, "action")
	var executable string
	var prefix []string
	if action == "login" {
		app.s.mu.Lock()
		path := app.s.state.Settings.Claude
		if agent == "chatgpt" {
			path = app.s.state.Settings.Codex
		}
		app.s.mu.Unlock()
		var err error
		executable, prefix, err = resolveRunner(path, agent)
		if err != nil {
			return nil, err
		}
	}
	script, err := setupScript(agent, action, executable, prefix)
	if err != nil {
		return nil, err
	}
	if err := openSetupWindow(encodePowerShell(script)); err != nil {
		return nil, fmt.Errorf("could not open the setup window: %w", err)
	}
	return M{"ok": true, "message": "A Windows setup window opened. Finish there, then click Check connections."}, nil
}

func encodePowerShell(script string) string {
	chars := utf16.Encode([]rune(script))
	data := make([]byte, len(chars)*2)
	for i, v := range chars {
		binary.LittleEndian.PutUint16(data[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(data)
}
