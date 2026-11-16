package main

import (
	"context"
	"testing"
	"time"
)

func switchFixture(t *testing.T) (*Store, *Goal) {
	s, g := fixture(t)
	g.AutoSwitch = true
	g.Team = &TeamRun{Status: "running", AutoSwitch: true, Agents: map[string]*RunnerState{"claude": {}, "chatgpt": {}}}
	return s, g
}
func TestAutoSwitchTransferAndSoloCompletion(t *testing.T) {
	for _, failed := range []string{"claude", "chatgpt"} {
		t.Run(failed, func(t *testing.T) {
			s, g := switchFixture(t)
			survivor := "claude"
			if failed == survivor {
				survivor = "chatgpt"
			}
			g.Tasks = []Task{{ID: "open", Title: "Remaining work", Owner: failed, Status: "doing", Lease: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}, {ID: "done", Title: "Existing result", Owner: failed, Status: "done", Evidence: "Keep this evidence"}}
			g.Votes = map[string]string{failed: "old approval"}
			g.Summary = "Shared saved context"
			if err := s.autoSwitchLocked(g, failed, survivor, "fixture connection failure"); err != nil {
				t.Fatal(err)
			}
			if g.Tasks[0].Owner != survivor || g.Tasks[1].Owner != failed || g.Tasks[1].Evidence != "Keep this evidence" || len(g.Votes) != 0 {
				t.Fatal("handoff lost ownership or evidence")
			}
			if _, err := s.call(context.Background(), survivor, "bridge_finish", M{"goal_id": g.ID, "evidence": "Premature"}); err == nil {
				t.Fatal("unfinished task was ignored")
			}
			call(t, s, g, survivor, "bridge_task", M{"op": "done", "task_id": "open", "evidence": "Reviewed and finished inherited work"})
			if _, err := s.call(context.Background(), failed, "bridge_finish", M{"goal_id": g.ID, "evidence": "Wrong agent"}); err == nil {
				t.Fatal("failed agent completed solo run")
			}
			call(t, s, g, survivor, "bridge_finish", M{"evidence": "Completed alone, no peer review"})
			if g.Status != "completed" || len(g.Votes) != 1 || g.Votes[failed] != "" {
				t.Fatal("solo completion invented peer approval")
			}
			restored, err := newStore(s.dir)
			if err != nil || !restored.state.Goals[0].AutoSwitch {
				t.Fatal("lost saved setting", err)
			}
		})
	}
}
func TestAutoSwitchGuards(t *testing.T) {
	for _, guard := range []string{"off", "paused", "uncertain", "busy-tool", "already-solo"} {
		t.Run(guard, func(t *testing.T) {
			s, g := switchFixture(t)
			switch guard {
			case "off":
				g.AutoSwitch = false
			case "paused":
				g.Status = "paused"
			case "uncertain":
				g.MCPServers = []string{"fixture"}
				s.state.MCPWarnings["fixture"] = "unknown edit"
			case "busy-tool":
				c := s.runtime.hub.connection("fixture")
				c.goal = g.ID
				c.busy = true
			case "already-solo":
				g.Team.SoloAgent = "chatgpt"
			}
			if err := s.autoSwitchLocked(g, "claude", "chatgpt", "fixture"); err == nil {
				t.Fatal("guard bypassed", guard)
			}
		})
	}
	s, g := fixture(t)
	g.Summary = "Done"
	call(t, s, g, "claude", "bridge_finish", M{"evidence": "One confirmation"})
	if g.Status == "completed" {
		t.Fatal("normal mode lost two-agent review")
	}
}
