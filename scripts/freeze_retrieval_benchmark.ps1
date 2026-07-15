param(
    [string]$BenchmarkId = "retrieval_v3_20260715",
    [string]$Version = "retrieval_v3",
    [string]$SourceRunId = "phase6c_quality_v2_20260715",
    [int]$Cases = 240,
    [int]$DevelopmentCases = 80,
    [long]$Seed = 20260715
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BackendRoot = Join-Path $ProjectRoot "backend-go"
$OutputDir = Join-Path $ProjectRoot "evaluation/benchmarks/retrieval_v3"
$PrivateOutputDir = Join-Path $ProjectRoot "evaluation/private/retrieval_v3"

& (Join-Path $PSScriptRoot "migrate.ps1")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

if (-not $env:POSTGRES_DSN) {
    $env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
}

Push-Location $BackendRoot
try {
    go run ./cmd/evalfreeze `
        --benchmark-id=$BenchmarkId `
        --version=$Version `
        --source-run-id=$SourceRunId `
        --cases=$Cases `
        --development-cases=$DevelopmentCases `
        --seed=$Seed `
        --output-dir=$OutputDir `
        --private-output-dir=$PrivateOutputDir
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally {
    Pop-Location
}
