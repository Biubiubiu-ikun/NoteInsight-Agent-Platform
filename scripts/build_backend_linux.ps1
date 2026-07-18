$ErrorActionPreference = "Stop"

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BackendRoot = Join-Path $ProjectRoot "backend-go"
$BinDir = Join-Path $BackendRoot "bin"
$PortableGo = Join-Path $ProjectRoot ".tools/go1.26.5/go/bin/go.exe"
$GoCommand = if (Test-Path -LiteralPath $PortableGo) { $PortableGo } else { "go" }
$PreviousCGOEnabled = $env:CGO_ENABLED
$PreviousGOOS = $env:GOOS
$PreviousGOARCH = $env:GOARCH

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

$GoVersionText = (& $GoCommand env GOVERSION).TrimStart("go")
$GoVersion = [Version]$GoVersionText
$SafeGoVersion = ($GoVersion.Major -gt 1) `
    -or ($GoVersion.Major -eq 1 -and $GoVersion.Minor -gt 26) `
    -or ($GoVersion.Major -eq 1 -and $GoVersion.Minor -eq 26 -and $GoVersion.Build -ge 5) `
    -or ($GoVersion.Major -eq 1 -and $GoVersion.Minor -eq 25 -and $GoVersion.Build -ge 12)
if (-not $SafeGoVersion) {
    throw "Go $GoVersionText contains known fixed vulnerabilities; use Go 1.25.12, 1.26.5, or newer."
}
Write-Host "Building Linux binaries with $(& $GoCommand version)"

Push-Location $BackendRoot
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"

    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/creatorinsight-api" ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw "API Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/creatorinsight-worker" ./cmd/worker
    if ($LASTEXITCODE -ne 0) { throw "Worker Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-simulator" ./cmd/simulator
    if ($LASTEXITCODE -ne 0) { throw "Simulator Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-corpusgen" ./cmd/corpusgen
    if ($LASTEXITCODE -ne 0) { throw "Corpus generator Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-seedgen" ./cmd/seedgen
    if ($LASTEXITCODE -ne 0) { throw "Seed generator Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/creatorinsight-migrate" ./cmd/migrate
    if ($LASTEXITCODE -ne 0) { throw "Migration Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-reconcile" ./cmd/reconcile
    if ($LASTEXITCODE -ne 0) { throw "Reconcile Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-materialize" ./cmd/materialize
    if ($LASTEXITCODE -ne 0) { throw "Fact materialization Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-maintenance" ./cmd/maintenance
    if ($LASTEXITCODE -ne 0) { throw "Maintenance Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-dlqctl" ./cmd/dlqctl
    if ($LASTEXITCODE -ne 0) { throw "DLQ control Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-evalfreeze" ./cmd/evalfreeze
    if ($LASTEXITCODE -ne 0) { throw "Evaluation benchmark freeze Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-datasetfreeze" ./cmd/datasetfreeze
    if ($LASTEXITCODE -ne 0) { throw "Dataset snapshot Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-evidence" ./cmd/evidence
    if ($LASTEXITCODE -ne 0) { throw "Evidence ingestion Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-retrieval-index" ./cmd/retrievalindex
    if ($LASTEXITCODE -ne 0) { throw "Lexical retrieval index Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-vector-index" ./cmd/vectorindex
    if ($LASTEXITCODE -ne 0) { throw "Vector retrieval index Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-retrieval-search" ./cmd/retrievalsearch
    if ($LASTEXITCODE -ne 0) { throw "Retrieval search Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-retrieval-eval" ./cmd/retrievaleval
    if ($LASTEXITCODE -ne 0) { throw "Retrieval evaluation Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-eval-dataset" ./cmd/evaldataset
    if ($LASTEXITCODE -ne 0) { throw "Evaluation dataset Linux build failed." }
    & $GoCommand build -trimpath -ldflags="-s -w" -o "bin/noteinsight-benchmark-audit" ./cmd/benchmarkaudit
    if ($LASTEXITCODE -ne 0) { throw "Benchmark audit Linux build failed." }
}
finally {
    $env:CGO_ENABLED = $PreviousCGOEnabled
    $env:GOOS = $PreviousGOOS
    $env:GOARCH = $PreviousGOARCH
    Pop-Location
}
