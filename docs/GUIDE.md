# L-Bridge connection guide

One prompt, two working agents, shared local tools. Start Team launches Claude Code and OpenAI Codex from L-Bridge, while the original desktop plugins remain available for manual chats.

## Easy Windows setup in v0.3

Download L-Bridge.exe and keep it in a folder. Double-click to open its local browser interface. No Python, Go or Node is required for L-Bridge itself. This is a portable Windows x64 EXE, not an MSI installer; it does not add a Start menu entry.

The first visit opens **Team & tools**:

1. Click **Install Claude Code**. Finish the official installer in the visible Windows window.
2. Click **Install Codex**. If Node.js is missing, its official download page opens. Install Node.js LTS, then click Install Codex again.
3. Click **Sign in** on each card and complete the provider's login. L-Bridge does not collect passwords.
4. Click **Check connections**. Installed programs are detected from PATH and standard install locations. Advanced paths are available if needed.
5. Create your goal and click **Start Team**. Provider accounts, internet access and usage limits still apply.

For shared MCP, use **Add MCP server**, enter a name, command and one argument per line, then save and **Test connection**. The **Roblox Studio** button saves the built-in preset directly. Enable each server under **Tools for your goal**, and enable Files + commands access before use. Studio must be open with its MCP server enabled. Local stdio MCP is supported; remote HTTP MCP is not included. Desktop host plugins remain under Connections.

Installation/sign-in buttons launch visible Windows PowerShell windows after you click them. Claude uses its official native installer; Codex uses its official npm package. Setup does not bypass PowerShell execution policies. Older manual npm instructions are no longer the preferred Claude installation route.

Official setup sources: https://code.claude.com/docs/en/setup and https://developers.openai.com/codex/cli/ .

## Port occupied after closing the tab

The browser is only the interface: closing its tab leaves the local service running. Earlier versions could treat a different running version as unavailable and then collide with its port.

With **0.2.1**, close connected hosts and double-click **Restart L-Bridge.cmd** in the newly extracted folder. It requests a graceful stop from the authenticated L-Bridge service, waits for shutdown, and starts this release using the saved data. **Stop L-Bridge.cmd** exits without restarting. The executable also offers a restart on Windows when it finds an older version; choosing No reopens that running version. A newer service is reopened rather than downgraded.

The helpers never kill a process merely because it owns a port. If another application occupies the default port, L-Bridge automatically selects a free loopback port and both adapters discover it. An explicit `--port NUMBER` is respected without fallback. An unmatched older bridge is not bypassed: stop that bridge first or correct the data-folder configuration. If an old plugin keeps relaunching the old service, close that host and update the plugin package before reopening it. Reinstalling the EXE alone does not replace binaries inside old plugins.

If you still have 0.1/0.2.0, reopen that executable and use **Settings → Stop local bridge**. If the interface is inaccessible, close connected hosts and end the identified **L-Bridge.exe** process in Task Manager before launching the new release.

## Both plugins report a port or connection error

Update **both plugin packages**, not only the standalone EXE. The new adapters reuse a compatible running service, find its actual port, and reconnect on the next tool call after it restarts. Two simultaneous host connections share one service. Readiness checks no longer wait for a busy conversation store. A failed or interrupted tool call is not automatically replayed; inspect its outcome before repeating an edit.

If you use a custom data folder, copy the refreshed configuration from **Connections** into both hosts. It now includes `--data-dir` with the exact folder used by the GUI. Bundled plugins use the default data folder unless their environment/configuration is customized; the manual configuration is the direct way to point them to a custom folder. Run both hosts under the same Windows user.

The local browser address is the UI, not a remote MCP connector URL. These plugins use the executable over stdio. Restart both hosts after updating their packages/configuration. Start Team already passes the exact data folder to its runners.

## Start on Windows

1. Extract `L-Bridge-Windows.zip` into a folder you can keep.
2. Open `L-Bridge.exe`. It starts a local service, preferring `127.0.0.1:48471` and selecting a free loopback port if another application occupies it, then opens the interface in your default browser. The interface and service are embedded in the executable; no Python, Node, Go, model API key, or separate runtime is needed.
3. Create a goal. Leave the folder empty for a new local workspace, or enter an existing absolute folder path. Choose file access or files plus commands.
4. Open **Team & tools** and use **Install** and **Sign in** on both cards.
5. Click **Check connections**; advanced paths are optional.
6. Return to the goal and press **Start Team**. Both agents receive the shared prompt, negotiate roles, and work in bounded rounds. Your message box sends follow-up directions.
7. For Roblox, follow the shared MCP setup below before starting.

The L-Bridge interface itself needs no separate runtime. **Start Team requires the two installed agent runners and account access**; the EXE does not include Claude or an OpenAI model. The OpenAI runner is Codex, not automation of a ChatGPT browser tab. A native Claude install is supported; the standard npm Codex installation also needs Node. If you prefer manual desktop plugins, use Connections and paste the host starter into both hosts instead.

This is a developer beta. The Windows binary is cross-compiled for x64 and is unsigned. The new runner and Roblox workflow has not been executed on a native Windows machine or against both live providers during this build. Automated tests use deterministic runner fixtures; see TESTING.md. Do not disable system protections to install it; organizations requiring signed software should build and sign it through their normal release process.

The interface is a browser-based local GUI, not a native Windows window. Closing the browser tab does not stop the service. Use Settings → Stop local bridge. Opening the same or an older executable reopens the existing service; a newer Windows executable offers a graceful restart.

## Claude Desktop

Install `L-Bridge-Claude-Windows.mcpb` through Claude Desktop's extension settings: Settings → Desktop app → Extensions → Advanced settings → Install Extension. Menu labels can vary by host version. The package includes its own binary and needs no model API key.

Enable the extension in a Claude conversation and use the host starter. The adapter runs with the identity `claude` and discovers the same local service used by the GUI. For a manual MCP configuration, the Connections screen provides the exact command for your installed executable. Merge the entry with existing MCP servers rather than replacing your configuration.

## ChatGPT / Codex desktop

`L-Bridge-OpenAI-Windows.zip` is the portable plugin package (root `plugin.json`, `mcp.json`, assets, and binary). Its adapter identity is `chatgpt`. It is intended for desktop hosts supporting local stdio MCP plugins.

The Windows distribution also contains a ready-to-use local marketplace under `OpenAI-local-source/`:

- `.agents/plugins/marketplace.json` is the marketplace index.
- `plugins/l-bridge/` is the plugin.

Open `OpenAI-local-source` as a local project in a compatible desktop host, then select the **L-Bridge Local** source in its plugin directory and install L-Bridge. Restart the host if it has not refreshed local sources. Alternatively, copy `plugins/l-bridge` into an existing project's `plugins/` folder and merge its marketplace entry into that project's `.agents/plugins/marketplace.json`. Paths are relative to the project root.

Local plugin availability depends on the host, account, and organization policy. Public-directory distribution of local MCP plugins requires OpenAI's partner approval. This build is not published or approved by either vendor. ChatGPT web-only custom connectors cannot reach this loopback-only service. No tunnel or cloud relay is included.

The Connections screen also shows the raw MCP server entry for hosts with manual MCP setup. Codex CLI users can register the executable with their host's documented `mcp add` command and `--mcp --agent chatgpt` arguments; this is a compatibility route, not a claim that Codex and ChatGPT are identical products.

## Built-in walkthrough and help

Use **? Guide** in the top bar or **Settings → Learning & help → Open walkthrough** for the eight-step tutorial. It covers runners, prompting, workspace access, limits, Roblox/MCP, steering, review and privacy. Small information icons beside controls open focused explanations. Toggle **Show information icons** in Settings to hide or show them; the preference is saved in that browser. The full walkthrough remains available when icons are hidden.

## Start Team and shared MCP

Start Team launches two local child processes using the documented `claude -p` and `codex exec --json` interfaces. Saved CLI sign-ins remain with their providers; L-Bridge does not extract login tokens or store model API keys. Runners may use provider-hosted inference and incur usage under their configured accounts.

The coordinator runs both agents concurrently, with a fresh, compact goal snapshot each round. Original requirements, user directions, shared summary, tasks, artifacts, and recent peer work are included; older messages remain available through cursor sync. Defaults: eight rounds, 300 seconds per agent turn, and 120 bridge work actions. Two rounds without bridge work stop the team. Errors stop the team rather than silently substituting another provider. Both agents must confirm completion.

The UI shows runner status, bounded activity logs, and measured token usage when emitted by the runners. Stop Team and Pause cancel owned runner processes and bridge commands. A restart never resumes a team automatically. Add direction and press Start Team to continue after a pause, failure, or limit; round/turn limits reset for the new run, while the goal's action allowance remains cumulative.

Runner sessions use a temporary directory outside the project. Claude is given L-Bridge MCP tools with built-in tools disabled; Codex uses a read-only sandbox with its shell tool disabled and user config excluded. CLI authentication still uses its normal store. Models use bridge file/command/MCP tools to act on the selected goal. Per-run credentials restrict adapter calls to one goal and agent and are revoked when the turn ends. Temporary connection files are deleted. These controls do not isolate L-Bridge itself from other programs under the same OS account. Managed provider policies remain applicable.

### Roblox Studio

1. Update Roblox Studio and open the intended place.
2. In Studio, open **Assistant → Manage MCP Servers → Enable Studio as MCP server**.
3. In L-Bridge, open **Team & tools → + Roblox Studio → Save connections**.
4. Use **Test connection** to discover the Studio tools.
5. In **Tools for [your goal]**, select Roblox Studio and save. Set the goal's workspace access to **Files + commands**.
6. Press **Start Team**. For multiple open places, specify which one in your prompt; the agents must select its Studio instance ID.

L-Bridge owns one stdio MCP connection per configured server. Agents discover tools through `bridge_mcp`, acquire an exclusive editing lease, and hold it across reading, editing and testing. Calls are serialized. Leases expire after three idle minutes and renew on each call; acquiring a new lease requires rereading the scene. This protects operations routed through L-Bridge, not human edits or separate direct MCP connections. Configure each external app once and avoid other concurrent writers.

Tool results are saved locally as shared receipts. Either agent can retrieve the complete result, including images, so one agent can review what the other actually observed. The conversation includes a short preview. Results up to the transport's 8 MiB message limit are supported; there is no sampling or server-requested execution.

An interrupted or disconnected tool call has an unknown outcome: it might already have edited Studio. The bridge records that uncertainty, blocks further calls and preserves the warning across restart. Inspect Studio, stop the team, then use **Reviewed · reconnect**. The bridge never retries the edit automatically.

Other trusted local stdio MCP servers can be added in the connections JSON with `id`, `name`, `command` and `args`. Enable each server separately for a goal. Remote HTTP MCP and arbitrary desktop chat wake-up are not implemented. In manual plugin mode, each host still needs to be started/resumed by its user.

## Features

- Start/stop both CLI runners from the GUI, with bounded rounds, progress and failure reporting.
- Shared local MCP tools, exclusive editing leases, Roblox preset, and cross-agent result receipts.
- Multiple local goals, each with its original prompt and chosen workspace.
- Negotiated roles; no hardcoded builder/reviewer requirement.
- Ordered, persistent messages with retry deduplication through sender nonces.
- Cursor-based synchronization with pagination, so completed messages need not be resent.
- Compact messages in any mutually understood language or structured format, paired with human-readable English summaries.
- A versioned shared summary with conflict checks.
- Task ownership and 20-minute renewable claims to avoid duplicate work.
- Workspace file list/read/write tools. SHA-256 compare-and-swap prevents silent overwrites by the other agent.
- Local command execution with bounded output, timeouts, and pause-triggered cancellation.
- Pause/resume, action allowances, transcript export, raw-message toggle, keyboard goal switcher, and local demo.
- Completion requires both agents' confirmations after all tasks are done. Further work invalidates previous confirmations. This remains a model judgment, not independent proof of correctness.

## Context efficiency and usage

The default allowance is 120 bridge work actions (messages, summary updates, task mutations, file writes, command/tool starts, and completion confirmations). Reaching it pauses new work. Reads, joins, and syncs do not consume this allowance. The user can change it and resume.

This is not a token or dollar cap. Manual plugins do not expose reliable host usage. Managed runners can report input/output/cache tokens, which L-Bridge displays for that run. It also reports message bytes and bridge actions. Subscription quotas and actual bills are not available, so it never reports invented savings.

Agents are instructed to reduce repeated context, share artifact paths/hashes, and use concise explicit messages. They may choose another language or format when that actually helps; compressed-looking strings can increase model token usage. This release does not automatically benchmark languages or train a private encoding. The human summary is written by the already-running agents, with no separate summarization model call.

## Storage and permissions

By default, service state and authentication material live in the current OS user's configuration folder under `L-Bridge` (on Windows, usually `%APPDATA%\L-Bridge`). Each goal can place artifacts in its own chosen local folder. All workspaces currently share the local service's transcript store. Cloud synchronization is not implemented; choosing a folder managed by another sync application is the user's choice.

`state.json` contains goals, messages, roles, tasks, artifact metadata, connection settings, run status, and MCP uncertainty warnings. `mcp-results/` stores shared tool receipts; they can contain project content and images. Goal JSON exports include receipt IDs/previews, not full image bytes. Runner activity retains the last 80 events; model reasoning and raw initialization records are not published. `endpoint.json` contains a per-service local token. Do not share the latter. Exported goal JSON contains the full prompt, messages, root path, summaries and metadata; review it before sharing.

The server binds only to loopback, validates HTTP Host and Origin, requires a random bearer token for data routes, and serves a restrictive content security policy. There is no telemetry or remote asset loading. Local storage does not make Claude or ChatGPT inference local: content agents read becomes part of their provider-hosted conversations.

File tools reject traversal, symlinks and special files and use Go's traversal-resistant `os.Root` operations. Files read/written through the bridge are limited to 512 KiB. This is not a security boundary against other programs running as the same OS user.

When **Files + commands** is enabled, commands run with the OS user's permissions. The selected workspace is the working directory, not a sandbox. Commands can access anything that user can access, including the network. Do not use background or detached jobs. Command timeout is 1–120 seconds with 32 KiB captured output. On Unix, cancellation kills the command's process group; on Windows it invokes `taskkill /T /F` for the running process tree. Detached programs and jobs that outlive their Windows parent may escape this cleanup. Host-native tools are controlled by the host; pausing L-Bridge cannot cancel separate tools launched outside L-Bridge.

## Build and verify

Requires Go 1.26+ and Python 3 for packaging and integration fixtures. The bridge has no third-party Go dependencies. Managed execution additionally requires the external agent runners.

```sh
go test -race ./...
go vet ./...
go build -o ../L-Bridge-dev .
python3 scripts/team_integration.py ../L-Bridge-dev
python3 scripts/build.py ../release
```

The build script produces a Windows x64 executable, Claude MCPB, OpenAI plugin ZIP, source ZIP, complete Windows ZIP, checksums, and a solid-compressed complete archive suitable for sharing by download link. An Apple Silicon development binary can be built with `go build -o L-Bridge .` on a Mac.

Two-process MCP integration test:

```sh
./L-Bridge --serve --no-open --data-dir ./test-data
python3 scripts/integration.py ./L-Bridge ./test-data/endpoint.json
```

Use a disposable test-data directory. The test creates a goal and artifact, launches separate `claude` and `chatgpt` MCP processes, exchanges messages, reads a shared artifact, and completes the confirmation handshake. It does not invoke live models.

Developer options: `--data-dir PATH`, `--port NUMBER`, `--no-open`, `--serve`, `--restart`, `--stop`, `--mcp`, `--agent claude|chatgpt`. `LBRIDGE_HOME` can set the data directory. All adapters must use the same directory. Keep one running service per data directory. A process-owned lock prevents two updated services from opening the same data directory, including when the preferred port is busy. The lock is released by the OS on exit or crash. If a previous run stopped unexpectedly, relaunch to restore saved goals; in-flight commands are not resumed.

## Upgrade from 0.1 or 0.2.0

Stop the old local bridge in Settings and close both hosts before replacing executables or updating plugins. Back up the existing L-Bridge data directory. Use Restart L-Bridge.cmd from the v0.2.1 folder, then update any installed local plugins to the included v0.2.1 packages. The new version reads existing goals and adds default runner settings; it does not move or remove project files. Keep the backup if you later roll back. Do not run old and new versions together against the same data directory.

## Official integration references

- [OpenAI: Non-interactive Codex and saved CLI authentication](https://developers.openai.com/codex/noninteractive)
- [Claude Code: Programmatic execution](https://code.claude.com/docs/en/headless)
- [Claude Code: CLI options](https://code.claude.com/docs/en/cli-reference)
- [Roblox: Built-in Studio MCP setup](https://create.roblox.com/docs/studio/mcp)
- [OpenAI: Package your plugin and local marketplaces](https://developers.openai.com/plugins/build/plugins)
- [OpenAI: Desktop-only plugin distribution](https://developers.openai.com/plugins/deploy/desktop-only)
- [Claude: Local MCP desktop extensions](https://support.claude.com/en/articles/10949351-getting-started-with-local-mcp-servers-on-claude-desktop)
- [MCP bundle manifest](https://github.com/modelcontextprotocol/mcpb/blob/main/MANIFEST.md)
- [MCP stdio transport](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)

L-Bridge is independent software; it is not an official OpenAI or Anthropic product.


## Recovering damaged local state

L-Bridge keeps the previous valid saved state as `state.json.bak` in the data folder shown in Settings. Before an update it also copies `state.json` to `state.before-update.json`.

If startup reports damaged JSON, stop L-Bridge and close connected hosts. Preserve the damaged file under another name, then copy the chosen backup to `state.json`. Restart and review the restored goal before continuing. A state backup restores the conversation and task bookkeeping; it does not undo files or edits in external applications. Do not share these backups publicly.
