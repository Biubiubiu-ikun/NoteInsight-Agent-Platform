param(
    [ValidateSet("ingest", "rebuild", "audit", "reconcile")]
    [string]$Operation = "ingest",
    [long]$DatasetVersionId = 2,
    [string]$RunId = "",
    [string]$Timeout = "45m"
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BackendRoot = Join-Path $ProjectRoot "backend-go"

if ($Operation -eq "audit" -and [string]::IsNullOrWhiteSpace($RunId)) {
    throw "RunId is required for an audit."
}
if (($Operation -eq "ingest" -or $Operation -eq "rebuild") -and $DatasetVersionId -le 0) {
    throw "DatasetVersionId must be positive."
}
if (-not $env:POSTGRES_DSN) {
    $env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@127.0.0.1:15432/creatorinsight?sslmode=disable"
}

$arguments = @(
    "run", "./cmd/evidence",
    "--operation=$Operation",
    "--dataset-version-id=$DatasetVersionId",
    "--timeout=$Timeout"
)
if (-not [string]::IsNullOrWhiteSpace($RunId)) {
    $arguments += "--run-id=$RunId"
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
