# Privacy and local data

L-Bridge stores goals, shared messages, summaries, task history and MCP receipts in the local data folder shown in Settings. Workspace files live in the folder selected for each goal. The browser interface uses loopback only, with a per-service token. There is no analytics service, cloud relay or automatic upload to this repository.

**Local coordination does not mean local AI.** Starting a team sends its prompt and relevant task context to the selected Claude and OpenAI runners. Their model providers process those requests under your account's terms and settings. Runners manage their own credentials; L-Bridge does not ask for your passwords. Installed runners and MCP servers may have their own logs and telemetry.

Web research is optional and off by default. Enabling it permits native search/page tools. Queries, visited addresses and relevant tool context can leave your PC. A prompt instruction tells the agents to avoid putting secrets and personal details in queries; this is not a technical guarantee against disclosure. Do not give agents material you cannot share with their providers. Turning Web research off is not a network firewall: enabled commands and MCP servers can also access the network.

Export saves a full goal transcript. Inspect it before sharing. Never publish the local data folder, `endpoint.json`, `state.json`, state backups, runner temporary directories, MCP receipts, `.env` files, credentials or personal projects. Screenshots can reveal workspace paths and messages; use the scripted demo for public screenshots.

Public distributions contain application source, generic documentation and compiled binaries. The release packager uses an explicit source-file selection and excludes runtime data, credentials and Git internals. Public Git commits use project-only author metadata. GitHub still displays the repository owner's public account and release publisher.

Before sharing a bug report, remove tokens, account details, local usernames, private paths and task contents. Prefer a minimal invented example. L-Bridge does not automatically send diagnostic reports.
