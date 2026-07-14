$ErrorActionPreference = "Stop"

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BackendRoot = Join-Path $ProjectRoot "backend-go"

Push-Location $BackendRoot
try {
    if (-not $env:POSTGRES_DSN) {
        $env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
    }
    if (-not $env:REDIS_ADDR) {
        $env:REDIS_ADDR = "localhost:6379"
    }

    go run ./cmd/migrate
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}
finally {
    Pop-Location
}
