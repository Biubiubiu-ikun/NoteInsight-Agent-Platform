$ErrorActionPreference = "Stop"

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BackendRoot = Join-Path $ProjectRoot "backend-go"
$BinDir = Join-Path $BackendRoot "bin"
$PreviousCGOEnabled = $env:CGO_ENABLED
$PreviousGOOS = $env:GOOS
$PreviousGOARCH = $env:GOARCH

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

Push-Location $BackendRoot
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"

    go build -trimpath -ldflags="-s -w" -o "bin/creatorinsight-api" ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw "API Linux build failed." }
    go build -trimpath -ldflags="-s -w" -o "bin/creatorinsight-worker" ./cmd/worker
    if ($LASTEXITCODE -ne 0) { throw "Worker Linux build failed." }
    go build -trimpath -ldflags="-s -w" -o "bin/noteinsight-simulator" ./cmd/simulator
    if ($LASTEXITCODE -ne 0) { throw "Simulator Linux build failed." }
    go build -trimpath -ldflags="-s -w" -o "bin/noteinsight-corpusgen" ./cmd/corpusgen
    if ($LASTEXITCODE -ne 0) { throw "Corpus generator Linux build failed." }
    go build -trimpath -ldflags="-s -w" -o "bin/noteinsight-seedgen" ./cmd/seedgen
    if ($LASTEXITCODE -ne 0) { throw "Seed generator Linux build failed." }
    go build -trimpath -ldflags="-s -w" -o "bin/creatorinsight-migrate" ./cmd/migrate
    if ($LASTEXITCODE -ne 0) { throw "Migration Linux build failed." }
    go build -trimpath -ldflags="-s -w" -o "bin/noteinsight-reconcile" ./cmd/reconcile
    if ($LASTEXITCODE -ne 0) { throw "Reconcile Linux build failed." }
}
finally {
    $env:CGO_ENABLED = $PreviousCGOEnabled
    $env:GOOS = $PreviousGOOS
    $env:GOARCH = $PreviousGOARCH
    Pop-Location
}
