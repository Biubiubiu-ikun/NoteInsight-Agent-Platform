param(
    [string]$BenchmarkId = "retrieval_v4_20260716",
    [string]$Version = "retrieval_v4",
    [string]$SourceRunId = "phase6c_quality_v2_20260715",
    [int]$Cases = 240,
    [int]$DevelopmentCases = 80,
    [long]$DatasetVersionId = 0,
    [string]$InputFile = ""
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BackendRoot = Join-Path $ProjectRoot "backend-go"
$OutputDir = Join-Path $ProjectRoot "evaluation/benchmarks/retrieval_v4"
$PrivateOutputDir = Join-Path $ProjectRoot "evaluation/private/retrieval_v4"
if (-not $InputFile) {
    $InputFile = Join-Path $PrivateOutputDir "authored_cases.jsonl"
}
if (-not (Test-Path -LiteralPath $InputFile)) {
    throw "Private authored benchmark input not found: $InputFile"
}
if ($DatasetVersionId -le 0) {
    throw "DatasetVersionId must identify a frozen dataset snapshot."
}

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
        --seed=0 `
        --dataset-version-id=$DatasetVersionId `
        --input-file=$InputFile `
        --output-dir=$OutputDir `
        --private-output-dir=$PrivateOutputDir
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally {
    Pop-Location
}
