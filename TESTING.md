# L-Bridge 0.4.1 verification

Passed locally: Go race tests; auto-switch fixtures in both directions; task transfer, saved evidence, single-agent completion, both-failed and survivor-failed stopping, missing-executable fallback, disabled-mode behavior, and stop/review guards. Connection tests cover a large escaped JSON request, real MCP progress notifications, interrupted read recovery, no uncertain-edit replay, token rotation and competing startup. A real 0.4.0 to 0.4.1 local service upgrade preserves goals and messages.

The estimated progress bar and Auto switch setting were checked in the browser with synthetic tasks. CI repeats Go and connection tests on Windows and Linux. Live provider outages and host-specific Claude/ChatGPT desktop timeout behavior remain unverified; fixtures establish protocol behavior, not compatibility with every host version.

# L-Bridge 0.4.0 verification

Passed locally on macOS:

- Go race tests and go vet, including provider stream parsing, scoped runner tools, web on/off arguments and saved preference, MCP deadlines, workspace and service protections.
- Team integration with two deterministic runner programs: shared MCP transport, image receipts, completion handshake, stop, provider failure, peer work preservation and continuation of the same saved goal with prior error history.
- Simultaneous adapter connection, occupied-port fallback, reconnection after stop and crash, preserved goals, and no interference with the unrelated port owner.
- Real upgrade from the previously built 0.3.0 service to 0.4.0, on the same port, with the saved goal and messages retained.
- JavaScript syntax checks. The interface exposes per-goal web research, recovery history and the updater.

GitHub Verify passed on Linux and Windows: Go race tests, native service connection recovery, and JavaScript syntax. Windows PowerShell 5.1 passed updater syntax and six fixture scenarios: already current, missing digest, checksum mismatch, busy task, successful replacement and failed-start rollback. These fixtures use real temporary files while replacing network and process operations with test doubles. [Verification run](https://github.com/bytexv/L-Bridge/actions/runs/43978299531).

Browser checks passed for web opt-in on goal creation, saved on/off preference after reload, visible errors after navigation and background refresh, unsent directions after reload, and no console errors. The public screenshot uses only the built-in scripted demo.

The Windows x64 release is cross-compiled and unsigned. Automated runner fixtures are not live models. Full live Claude Code + Codex + Roblox execution, native installer/sign-in windows, live provider web research, and an end-to-end Windows update/rollback have not been verified here. Updater syntax and unit coverage do not prove replacement behavior on every Windows installation.

The app opens a local browser interface; this is a portable EXE, not an MSI or native-window installer. Subscription quotas and costs are not measured or guaranteed. CLI flags and provider features may change; blocked tools are reported rather than bypassed.
