param(
    [ValidateSet("dev", "capacity", "million-comments")]
    [string]$Profile = "capacity",
    [long]$Seed = 20260714,
    [switch]$Truncate,
    [switch]$WithTokens,
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$composeFile = Join-Path $projectRoot "docker-compose.yml"
$tokenDir = Join-Path $projectRoot "backend-go\tmp"
New-Item -ItemType Directory -Force -Path $tokenDir | Out-Null

if (-not $DryRun) {
    $services = docker compose -f $composeFile ps --status running --services
    foreach ($required in @("postgres", "redis")) {
        if ($services -notcontains $required) {
            throw "Required service '$required' is not running."
        }
    }
}

$seedArgs = @("--profile=$Profile", "--seed=$Seed", "--token-out=/output/dev_tokens.csv")
if ($Truncate) {
    $seedArgs += "--truncate"
}
if ($WithTokens) {
    $seedArgs += "--with-tokens"
}
if ($DryRun) {
    $seedArgs += "--dry-run"
}

$tokenMount = "${tokenDir}:/output"
$dockerArgs = @(
    "compose", "-f", $composeFile, "run", "--rm", "--no-deps",
    "-v", $tokenMount,
    "--entrypoint", "/app/noteinsight-seedgen",
    "backend"
) + $seedArgs

Write-Host "Generating '$Profile' data with seed $Seed."
& docker @dockerArgs
if ($LASTEXITCODE -ne 0) {
    throw "Capacity data generation failed with exit code $LASTEXITCODE."
}
