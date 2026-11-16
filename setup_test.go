package main

import (
	"strings"
	"testing"
)

func TestSetupRejectsUnknownActions(t *testing.T) {
	for _, x := range [][2]string{{"other", "install"}, {"claude", "run"}, {"chatgpt", "login"}} {
		if _, err := setupScript(x[0], x[1], "", nil); err == nil {
			t.Fatal("invalid setup accepted", x)
		}
	}
}
func TestSetupQuotesPathsAndArguments(t *testing.T) {
	s, err := setupScript("chatgpt", "login", `C:\Developer's tools\node.exe`, []string{`C:\$(malicious)\codex.js`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `'C:\Developer''s tools\node.exe' 'C:\$(malicious)\codex.js' 'login'`) {
		t.Fatal("paths not literal", s)
	}
}
func TestSetupUsesOfficialInstallers(t *testing.T) {
	for agent, target := range map[string]string{"claude": "https://claude.ai/install.ps1", "chatgpt": "npm.cmd install -g @openai/codex"} {
		s, err := setupScript(agent, "install", "", nil)
		if err != nil || !strings.Contains(s, target) {
			t.Fatal(agent, err)
		}
	}
}
