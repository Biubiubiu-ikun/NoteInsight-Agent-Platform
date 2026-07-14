param(
    [ValidateSet("dev", "capacity", "million-comments")]
    [string]$Profile = "capacity",
    [long]$Seed = 20260714,
    [string]$ComposeProject = "",
    [string]$ComposeEnvFile = "",
    [string]$TokenDirectory = "backend-go\tmp",
    [string]$TokenFileName = "dev_tokens.csv",
    [switch]$Truncate,
    [switch]$WithTokens,
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$composeFile = Join-Path $projectRoot "docker-compose.yml"
$tokenDir = Join-Path $projectRoot $TokenDirectory
New-Item -ItemType Directory -Force -Path $tokenDir | Out-Null
$composeArgs = @("compose")
if ($ComposeEnvFile) {
    $composeEnvPath = if ([IO.Path]::IsPathRooted($ComposeEnvFile)) { $ComposeEnvFile } else { Join-Path $projectRoot $ComposeEnvFile }
    $composeArgs += @("--env-file", $composeEnvPath)
}
$composeArgs += @("-f", $composeFile)
if ($ComposeProject) {
    $composeArgs += @("-p", $ComposeProject)
}

if (-not $DryRun) {
    $psArgs = $composeArgs + @("ps", "--status", "running", "--services")
    $services = & docker @psArgs
    foreach ($required in @("postgres", "redis")) {
        if ($services -notcontains $required) {
            throw "Required service '$required' is not running."
        }
    }
}

$seedArgs = @("--profile=$Profile", "--seed=$Seed", "--token-out=/output/$TokenFileName")
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
$dockerArgs = $composeArgs + @(
    "run", "--rm", "--no-deps",
    "-v", $tokenMount,
    "--entrypoint", "/app/noteinsight-seedgen",
    "backend"
) + $seedArgs

Write-Host "Generating '$Profile' data with seed $Seed."
& docker @dockerArgs
if ($LASTEXITCODE -ne 0) {
    throw "Capacity data generation failed with exit code $LASTEXITCODE."
}
