# OpenMessage on Windows — runbook

Operating guide for the Windows build of OpenMessage on `wes-desktop`: a
Windows service that syncs Google Messages (SMS/RCS), plus MCP clients in
Claude Code and Claude Desktop that read from it.

Everything here was verified on Windows 10 Pro 22H2 (build 19045) with Go 1.26
on 2026-09-25. The Windows support lives on the `windows-support` branch of
`github.com/Deathnerd/openmessage` (a fork of `MaxGhenis/openmessage`).

---

## 1. At a glance

| Thing | Value |
|---|---|
| Binary | `C:\tools\openmessage.exe` (on the system `PATH`) |
| Source checkout | `C:\Users\wesgi\Projects\openmessage` (branch `windows-support`) |
| Service name / display name | `OpenMessage` / `OpenMessage daemon` |
| Service command line | `"C:\tools\openmessage.exe" service` |
| Service account | `NT SERVICE\OpenMessage` (virtual account, no password) |
| Startup type | Automatic (Delayed Start) |
| Data directory | `C:\Users\wesgi\.local\share\openmessage` |
| Daemon log | `<data dir>\daemon.log` (rotated to `daemon.log.1` at 20 MiB, checked at service start) |
| Local API + web UI | `http://127.0.0.1:7007` (loopback only; MCP-over-SSE is off) |
| MCP server (stdio) | `C:\tools\openmessage.exe serve --mcp-stdio` |

**Quick health check** (any shell):

```powershell
Get-Service OpenMessage
Invoke-RestMethod http://127.0.0.1:7007/api/status | Select-Object connected, google, backfill
```

Healthy looks like `Status: Running` and `google.connected = True`,
`google.paired = True`, `google.needs_pairing = False`.

---

## 2. How it fits together

```
Android phone ──(Google Messages web protocol, via libgm)──► OpenMessage service
                                                              (openmessage.exe service)
                                                               │  owns the Google session
                                                               │  writes messages.db
                                                               ▼
                                          C:\Users\wesgi\.local\share\openmessage\
                                          ├─ session.json   (paired Google credentials)
                                          ├─ messages.db    (+ -wal, -shm)
                                          ├─ control.token  (local API auth token)
                                          └─ daemon.log
                                                               ▲ reads store directly
                                                               │ sends/status via :7007
Claude Code / Claude Desktop ──stdio──► openmessage.exe serve --mcp-stdio  (one per session)
```

Key rules:

- **Exactly one daemon.** Only the service may hold the live Google
  connection. Running `openmessage serve` by hand while the service runs means
  two clients fight over one pairing and the session is lost. Stop the
  service first (section 6).
- **MCP processes are clients.** `serve --mcp-stdio` starts no transports. It
  reads `messages.db` directly and sends messages through the daemon's API on
  `127.0.0.1:7007`. If the daemon is down, the MCP server still starts, but
  `get_status` shows `daemon_reachable: false` and the inbox is empty or stale.
- **Data dir discovery.** MCP clients run as `wesgi` and default to
  `%USERPROFILE%\.local\share\openmessage`. The service runs as a different
  account, so its data dir is pinned with `OPENMESSAGES_DATA_DIR` in its
  service environment (section 3.3). Clients also adopt the `data_dir` the
  daemon reports in `/api/status`.

---

## 3. Install from scratch

Run from an **elevated** PowerShell 7 (`pwsh`). Allow about 15 minutes, plus
the first full sync (roughly 10–30 minutes depending on message history).

### 3.1 Build

```powershell
git clone git@github.com:Deathnerd/openmessage.git C:\Users\wesgi\Projects\openmessage
Set-Location C:\Users\wesgi\Projects\openmessage
git switch windows-support
go test ./...                      # expect every package "ok"
go build -ldflags "-X main.version=windows-$(git rev-parse --short HEAD)" -o openmessage.exe .
Copy-Item .\openmessage.exe C:\tools\openmessage.exe -Force
(Get-Command openmessage).Source   # -> C:\tools\openmessage.exe
```

`C:\tools` is on the **machine** `PATH`, which every user (including service
accounts) inherits, so there's nothing to add to the user `PATH`.

### 3.2 Pair with the phone

Pairing writes `session.json` into the data dir. Do it as `wesgi`, **before**
creating the service, or with the service stopped:

```powershell
openmessage pair            # QR code: phone → Google Messages → Device pairing → scan
# or, Google-account pairing from browser cookies:
openmessage pair --google-file <cookies.json>
```

Verify: `Test-Path $env:USERPROFILE\.local\share\openmessage\session.json` → `True`.

### 3.3 Create the service

```powershell
$name    = 'OpenMessage'
$dataDir = 'C:\Users\wesgi\.local\share\openmessage'

New-Service -Name $name -DisplayName 'OpenMessage daemon' `
  -Description 'Google Messages sync daemon + local API for the OpenMessage MCP server. Runbook: C:\Users\wesgi\Projects\openmessage\docs\windows-runbook.md' `
  -BinaryPathName '"C:\tools\openmessage.exe" service' -StartupType Automatic

# Least-privilege virtual account; delayed start gives Wi-Fi time to come up.
sc.exe config $name obj= "NT SERVICE\$name" start= delayed-auto

# Restart on failure: 1 min, 1 min, then every 5 min; reset the count daily.
sc.exe failure $name reset= 86400 actions= restart/60000/restart/60000/restart/300000
sc.exe failureflag $name 1     # also treat non-zero exits (not just crashes) as failures

# Pin the data dir (the virtual account's own profile is elsewhere).
New-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\$name" -Name Environment `
  -PropertyType MultiString -Value @("OPENMESSAGES_DATA_DIR=$dataDir") -Force

# Give the service Modify on the data dir (inherits to new files).
icacls $dataDir /grant "NT SERVICE\${name}:(OI)(CI)M" /T

Start-Service $name
```

Verify:

```powershell
sc.exe qc OpenMessage            # SERVICE_START_NAME : NT SERVICE\OpenMessage, AUTO_START (DELAYED)
sc.exe qfailure OpenMessage      # three RESTART actions
Invoke-RestMethod http://127.0.0.1:7007/api/status | Select-Object -ExpandProperty google
Get-Content $dataDir\daemon.log -Tail 20   # "Service starting", "Connected to Google Messages"
```

### 3.4 Register the MCP server

**Claude Code** (user scope, all projects):

```powershell
claude mcp add openmessage -s user -- 'C:\tools\openmessage.exe' serve --mcp-stdio
claude mcp get openmessage       # Status: ✔ Connected
```

**Claude Desktop** — `%APPDATA%\Claude\claude_desktop_config.json`, under
`mcpServers` (back up the file first; restart Claude Desktop afterwards):

```json
"openmessage": {
  "command": "C:\\tools\\openmessage.exe",
  "args": ["serve", "--mcp-stdio"]
}
```

A backup from the original setup is at
`%APPDATA%\Claude\claude_desktop_config.json.bak-2026-09-25`.

Verify from a Claude session: call `get_status` (expect
`daemon_reachable: true`, `google.connected: true`), then
`list_conversations` with `limit: 3`.

---

## 4. Upgrading

Allow about 5 minutes. MCP clients hold `C:\tools\openmessage.exe` open while
Claude sessions run, so a copy can fail with "file in use" until they exit.

```powershell
Set-Location C:\Users\wesgi\Projects\openmessage
git fetch origin; git switch windows-support; git pull --ff-only
go test ./...
go build -ldflags "-X main.version=windows-$(git rev-parse --short HEAD)" -o openmessage.exe .

Stop-Service OpenMessage          # takes up to ~10 s, see §7.4
Copy-Item .\openmessage.exe C:\tools\openmessage.exe -Force
Start-Service OpenMessage
```

If the copy fails because the file is in use, close Claude Code and Claude
Desktop sessions (or find the holders with
`Get-Process openmessage | Select-Object Id, Path`), then retry. The running
MCP processes keep using the old binary until their session restarts.

### Pulling upstream changes

```powershell
git fetch upstream
git switch windows-support
git rebase upstream/main          # or merge; then rerun go test ./...
git push --force-with-lease origin windows-support
```

Watch for upstream re-introducing Unix-only calls (`syscall.Flock`,
`syscall.Statfs`, `syscall.Umask`, `file:` URIs built from raw paths, directory
`fsync`). A quick guard: `GOOS=windows go vet ./...` from any OS.

---

## 5. Day-to-day operations

| Task | Command |
|---|---|
| Status | `Get-Service OpenMessage` |
| Daemon health | `Invoke-RestMethod http://127.0.0.1:7007/api/status` |
| Follow the log | `Get-Content C:\Users\wesgi\.local\share\openmessage\daemon.log -Tail 50 -Wait` |
| Restart | `Restart-Service OpenMessage` (elevated) |
| Stop / start | `Stop-Service OpenMessage` / `Start-Service OpenMessage` (elevated) |
| Message counts + freshness | `openmessage status` (reads the store; safe while the service runs) |
| Search from a terminal | `openmessage read "query" --limit 20` |
| Show a thread | `openmessage thread "<name or number>" --limit 50` |
| Web UI | open the one-time `.../auth/bootstrap?t=...` URL printed in `daemon.log` after each start |

### Log level

Set `OPENMESSAGES_LOG_LEVEL=debug` in the service environment for verbose
logs. At debug level, a stop that times out also writes a full goroutine dump
to `daemon.log`.

```powershell
$key = 'HKLM:\SYSTEM\CurrentControlSet\Services\OpenMessage'
Set-ItemProperty $key -Name Environment -Value @(
  'OPENMESSAGES_DATA_DIR=C:\Users\wesgi\.local\share\openmessage',
  'OPENMESSAGES_LOG_LEVEL=debug')
Restart-Service OpenMessage
```

Remove the `OPENMESSAGES_LOG_LEVEL` entry and restart to go back to normal.

### Useful environment variables (service `Environment` value)

| Variable | Effect |
|---|---|
| `OPENMESSAGES_DATA_DIR` | Data directory. **Required** for the service. |
| `OPENMESSAGES_LOG_LEVEL` | `debug`, `info` (default), `warn`, `error`. |
| `OPENMESSAGES_PORT` | API/web port (default `7007`). MCP clients must see the same value. |
| `OPENMESSAGES_HOST` | Bind address. Leave unset (loopback). **Never** set to `0.0.0.0`. |
| `OPENMESSAGES_STARTUP_BACKFILL` | `off`, `shallow`, `deep`; default is deep on an empty store, otherwise shallow. |
| `OPENMESSAGE_TELEMETRY` | `1` opts in to an anonymous daily heartbeat. Off by default; leave it off. |

---

## 6. Running the daemon by hand (debugging)

```powershell
Stop-Service OpenMessage                           # never run two daemons
$env:OPENMESSAGES_DATA_DIR = 'C:\Users\wesgi\.local\share\openmessage'
openmessage serve                                  # logs to the console; Ctrl+C to stop
Start-Service OpenMessage                          # when done
```

`openmessage service` refuses to run from a console on purpose. Use `serve`
for foreground runs.

---

## 7. Troubleshooting

### 7.1 MCP `get_status` says `daemon_reachable: false`

`connectex: No connection could be made...` on `127.0.0.1:7007`.

1. `Get-Service OpenMessage`. If it's stopped, run `Start-Service OpenMessage` and
   read `daemon.log`.
2. If it's running but the port is closed, check `netstat -ano | findstr :7007`. Another
   process may own the port, or the service may be crash-looping (look for
   repeated `Service starting` lines in `daemon.log`).
3. If it fails immediately at boot, the network is probably late. Failure actions
   retry after 1 minute, then 1 minute, then every 5 minutes. On this machine
   Wi-Fi comes up roughly 90 s after logon, and it's 2.4 GHz only: joining
   `FLAMINGO_5G-2` breaks DNS.

### 7.2 Conversations empty, or the inbox is stale

- `openmessage status` shows per-platform counts and flags a platform that is
  `Nd behind`.
- Right after a first pairing, the deep backfill can take tens of minutes.
  Watch `backfill` in `/api/status` (`phase`, `conversations_found`,
  `messages_found`).
- Confirm the MCP client and the service use the same data dir: `data_dir`
  in `get_status` must be `C:\Users\wesgi\.local\share\openmessage`.

### 7.3 `google.needs_pairing: true`, or auth errors in the log

The Google session was revoked: the phone unpaired it, too many reconnects,
or a second daemon used the session.

```powershell
Stop-Service OpenMessage
Rename-Item C:\Users\wesgi\.local\share\openmessage\session.json session.json.old
openmessage pair                  # as wesgi, scan the QR code
icacls C:\Users\wesgi\.local\share\openmessage /grant "NT SERVICE\OpenMessage:(OI)(CI)M" /T
Start-Service OpenMessage
```

Don't loop reconnects. Google throttles accounts that reconnect repeatedly.
The upstream `docs/agent-runbook.md` covers Google-account (cookie) pairing
in depth.

### 7.4 Stopping the service takes about 10 seconds

Expected when a stop lands mid-sync. `daemon.log` shows:

```
WRN serve cleanup still blocked (usually an in-flight Google RPC); exiting without it
INF Service stopped
```

Cause: libgm (the Google Messages protocol library) waits for an RPC reply
with no deadline, and `Disconnect` doesn't release the wait, so cleanup
blocks. The service waits 10 s, then exits. No data is lost: SQLite in WAL
mode keeps every committed write across a process exit, and backfill resumes
on the next start. If stops start failing **when idle**, set the log level to
`debug` (section 5) to capture a goroutine dump.

### 7.5 `Access is denied` / `unable to open database file` in `daemon.log`

The service account lost rights on the data dir. Common causes are restoring
files from backup or re-creating the folder.

```powershell
icacls C:\Users\wesgi\.local\share\openmessage   # expect NT SERVICE\OpenMessage:(OI)(CI)(M)
icacls C:\Users\wesgi\.local\share\openmessage /grant "NT SERVICE\OpenMessage:(OI)(CI)M" /T
Restart-Service OpenMessage
```

### 7.6 `The process cannot access the file because it is being used by another process`

This is a Windows sharing violation around `session.json`. The Windows branch
retries these for up to about 1 s (`internal/fsretry`). If it still
happens, something external is holding the file, often antivirus or a backup
tool. Exclude the data dir from real-time scanning, or find the holder with
Sysinternals `handle.exe session.json` (in `C:\tools\sysinternals`).

### 7.7 Two daemons / session keeps dropping

```powershell
Get-CimInstance Win32_Process -Filter "Name='openmessage.exe'" | Select-Object ProcessId, CommandLine
```

Expect exactly one `... service` process plus one `... serve --mcp-stdio`
per open Claude session. Kill any stray `serve` process that lacks
`--mcp-stdio`.

### 7.8 Web UI shows "not authenticated" warnings

`WRN Local control request is not authenticated; allowed during
accept-and-log rollout` means a local request (curl, `Invoke-RestMethod`,
the web UI before bootstrap) came in without the control token. Upstream
currently runs auth in `accept-and-log` mode: such requests are allowed and
logged. To authorize a browser, open the bootstrap URL from the latest
`daemon.log` start.

---

## 8. Security notes

- **Blast radius.** The data dir holds full message history and live Google
  pairing credentials (`session.json`). Anyone who can read it can read your
  texts and impersonate the paired web session. Its ACL should list only
  `wesgi`, `SYSTEM`, `Administrators`, and `NT SERVICE\OpenMessage`. Check
  with `icacls`.
- **Service identity.** `NT SERVICE\OpenMessage` is a virtual account. It has
  no password, can't log on interactively, and has only the rights explicitly
  granted, which is Modify on the data dir. Don't switch the service to
  `LocalSystem`.
- **Network exposure.** The API binds `127.0.0.1:7007` only, and MCP-over-SSE
  is disabled. Local control auth is in `accept-and-log` mode, so any local
  process can call the API, including sending messages. Treat local code
  execution on this box as able to send texts.
- **Telemetry.** Off unless `OPENMESSAGE_TELEMETRY=1` is set. When on, it sends
  an anonymous daily heartbeat (install id, version, OS, which platforms are
  paired) to `openmessage.ai`, with no message content.
- **Supply chain.** The binary is built locally from the fork. libgm is a
  reverse-engineered protocol, and Google can break it at any time.
- **Uninstalling** doesn't delete data (section 9). Delete the data dir
  explicitly if you mean to.

---

## 9. Uninstall

```powershell
Stop-Service OpenMessage
sc.exe delete OpenMessage
claude mcp remove openmessage -s user
# Claude Desktop: remove the "openmessage" entry from claude_desktop_config.json
Remove-Item C:\tools\openmessage.exe
# Optional, irreversible — message history and pairing credentials:
# Remove-Item -Recurse C:\Users\wesgi\.local\share\openmessage
```

Also remove the paired device on the phone: Google Messages → Device pairing
→ remove the "OpenMessage" browser entry.

---

## 10. What the Windows branch changes (for reviewers)

Commits on `windows-support`, on top of upstream `main`:

1. **Build and run on Windows.** Build-tagged shims in
   `cmd/platform_{unix,windows}.go`:
   - `flock` → `LockFileEx`, on a byte past EOF so the lock record stays readable
   - `statfs` → `GetDiskFreeSpaceEx`
   - `umask` → no-op

   Also fixes:
   - `file:///C:/...` SQLite URIs
   - skip directory `fsync`
   - flush through a writable handle
   - blob dedup no longer checks Unix mode bits
2. **Windows races.**
   - `internal/fsretry` retries rename/read on sharing violations for
     `session.json`.
   - The link-preview LRU uses a monotonic sequence instead of timestamps that
     tie on Windows.
   - signal-cli temp confinement also sets `TMP`/`TEMP`.
3. **Test suite.** Unix assumptions in tests: mode bits, the `.exe` suffix,
   `#!` stubs → `.cmd`, `TMP`/`USERPROFILE`, a `?` filename, JSON-escaped
   paths.
4. **Windows service.**
   - `openmessage service` runs `serve` under the Service Control Manager,
     logging to `daemon.log`.
   - `serveStop` is the SCM's equivalent of SIGTERM.
   - `App.BeginShutdown` aborts backfill at its next checkpoint.
   - A 10 s bounded stop handles the libgm RPC hang.

Verification: `go test ./...` passes on Windows (33 packages).
`GOOS=linux` and `GOOS=darwin` `go vet ./...` pass.
