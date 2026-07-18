param(
    [ValidateSet("init", "prepare", "audit", "freeze", "status")]
    [string]$Operation = "status",
    [string]$ReviewerA = "",
    [string]$ReviewerB = "",
    [long]$DatasetVersionId = 2,
    [string]$IngestionRunId = "phase7a_dv2_rebuild_v2_20260718"
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$BackendRoot = Join-Path $ProjectRoot "backend-go"
$Workspace = Join-Path $ProjectRoot "evaluation/private/retrieval_v5"
$PublicRoot = Join-Path $ProjectRoot "evaluation/benchmarks/retrieval_v5"

if ($Operation -eq "prepare" -and (-not $ReviewerA -or -not $ReviewerB)) {
    throw "Prepare requires two distinct reviewer pseudonyms via -ReviewerA and -ReviewerB."
}
if (-not $env:POSTGRES_DSN) {
    $env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@127.0.0.1:15432/creatorinsight?sslmode=disable"
}

Push-Location $BackendRoot
try {
    $arguments = @(
        "run", "./cmd/benchmarkreview",
        "-operation", $Operation,
        "-workspace", $Workspace,
        "-public-root", $PublicRoot,
        "-dataset-version-id", $DatasetVersionId,
        "-ingestion-run-id", $IngestionRunId
    )
    if ($ReviewerA) { $arguments += @("-reviewer-a", $ReviewerA) }
    if ($ReviewerB) { $arguments += @("-reviewer-b", $ReviewerB) }
    & go @arguments
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally {
    Pop-Location
}
