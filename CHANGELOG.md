# L-Bridge 0.4.1 — Auto switch, plugin reliability and progress

- Add per-goal Auto switch (off by default) for Start Team: hand remaining work to the surviving runner after a peer failure or unavailable executable.
- Transfer unfinished task ownership and preserve shared context/evidence; explicitly label solo completion without inventing peer approval.
- Preserve stop controls, budgets and uncertain-MCP-edit review; stop if both runners fail.
- Add an Estimated progress bar based on task completion; wait for a task plan and reserve 100% for confirmed completion.
- Retry bounded read-only adapter requests after interrupted connections and refresh explicitly rejected desktop service tokens; never replay an uncertain mutation.
- Wait for a competing adapter's startup instead of treating an early contender exit as a disconnected service.
- Raise authenticated adapter/HTTP request limits to 8 MiB so valid escaped file content does not close the plugin connection.
- Send periodic progress notifications during long MCP calls when supported by the host.
- Preserve the tail of runner errors to keep the useful final error message visible.

# L-Bridge 0.4.0 — web research, recovery and updates

- Optional per-goal web research: Claude WebSearch/WebFetch and Codex live search, with shared source handoffs.
- Continue task starts a new attempt from the saved goal, tasks, conversation and prior error; retain the last ten attempts.
- A failed runner lets its peer finish its bounded turn instead of abruptly cancelling a possible edit.
- Persist request errors and unsent message drafts in the interface.
- Refuse continuation until uncertain MCP edits have been reviewed.
- Bound MCP writes by their request deadline, including a server that stops reading.
- Keep a previous valid state backup before saving.
- Reject slash-rooted file paths consistently on Windows as well as Unix.
- Add the Windows GitHub updater, GUI action, --update and Update L-Bridge.cmd; verify download digest, preserve state, keep a previous EXE and attempt startup rollback.
- Publish generic setup, privacy and recovery documentation with release download links.

# L-Bridge 0.3.0 beta — easier Windows setup

- First-run setup cards with Install, Sign in and Check connections buttons.
- Visible Windows setup/login windows; official native Claude installer and Codex npm installer.
- Node.js download guidance when Codex prerequisites are missing; Node discovery after installation.
- Plain MCP connection form; Roblox preset saved immediately; advanced JSON remains available.
- Check connections saves edited settings before probing agents.
- Direct portable EXE distribution alongside both desktop plugin packages and source.
- Existing port recovery, shared tool receipts, editing leases and tutorial settings retained.

# L-Bridge 0.2.1 beta — startup and connection recovery

- Recognize an authenticated older service instead of attempting to bind its occupied port.
- Offer a graceful restart during Windows upgrades, or reopen the running version if declined.
- Add Restart L-Bridge.cmd, Stop L-Bridge.cmd, --restart and --stop.
- Reuse compatible running services and handle simultaneous plugin connections.
- Select a free loopback port if another application occupies the default.
- Hold an OS-owned service lock per data folder, released on normal exit or crash.
- Include the GUI data directory in copied MCP configurations.
- Use authenticated lightweight readiness checks and clearer connection errors.
- Restrict endpoint URLs to loopback; do not follow redirects or stop unverified processes.
- Explain that closing the browser tab leaves the service running.
- Preserve existing saved goals and messages during restart.

# L-Bridge 0.2.0 beta

- Start Team launches Claude Code and Codex from one goal in the GUI.
- Automatic bounded rounds, stop/pause cancellation, interrupted-run recovery, and provider failure reporting.
- Live agent status and bounded activity feed; token usage is shown only when reported by runners.
- Shared local stdio MCP connections, with a Roblox Studio preset and per-goal access selection.
- Exclusive editing leases protect read/edit/test sequences; shared receipts preserve text and images for peer review.
- Ambiguous tool outcomes block further calls until the user reviews and reconnects; warnings survive restart.
- Scoped temporary adapter credentials, separate runner working directories, and no automatic restart after a crash.
- Built-in eight-step walkthrough and contextual information icons, with a persistent browser setting to hide/show help.
- Existing manual desktop plugins and v0.1 goal data remain supported.

Start Team requires installed, signed-in Claude Code and Codex CLI. The Windows executable does not include model runtimes or provider accounts. See README.md and TESTING.md for setup and verification limits.
