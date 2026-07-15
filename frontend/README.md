# NoteInsight Console

React + TypeScript testing console for the current NoteInsight APIs.

## Modules

```text
src/api/          HTTP client and JWT refresh handling
src/auth/         Login state and account dialogs
src/components/   Feed, detail, compose, ranking, profile, runtime views
src/lib/          Display and session helpers
src/types/        Backend API contracts
src/assets/       Local visual assets used when note media URLs are empty
```

## Start

Start PostgreSQL, Redis, NATS, API, and worker first, then run from the project
root:

```powershell
.\scripts\start_frontend.ps1
```

The console is available at `http://127.0.0.1:15173/`.

## Verify

```powershell
cd frontend
$env:PATH = (Resolve-Path "..\.tools\node").Path + [IO.Path]::PathSeparator + $env:PATH
& "..\.tools\node\node.exe" "..\.tools\node\node_modules\npm\bin\npm-cli.js" run build
```

The frontend proxies `/api` to the Go API, and the three `*-runtime` paths to
the API readiness, worker readiness/metrics, and NATS monitoring endpoints.
