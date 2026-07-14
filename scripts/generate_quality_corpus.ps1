param(
    [ValidateSet("smoke", "quality")]
    [string]$Profile = "quality",
    [long]$Seed = 20260714,
    [string]$RunId = "",
    [switch]$Replace
)

$ErrorActionPreference = "Stop"

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BackendRoot = Join-Path $ProjectRoot "backend-go"

& (Join-Path $PSScriptRoot "migrate.ps1")
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

if (-not $env:POSTGRES_DSN) {
    $env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
}

$arguments = @(
    "run", "./cmd/corpusgen",
    "--profile=$Profile",
    "--seed=$Seed",
    "--strict=true"
)
if ($RunId) {
    $arguments += "--run-id=$RunId"
}
if ($Replace) {
    $arguments += "--replace"
}

Push-Location $BackendRoot
try {
    & go @arguments
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}
finally {
    Pop-Location
}
