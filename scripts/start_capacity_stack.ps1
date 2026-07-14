param(
    [string]$ComposeProject = "noteinsight-capacity",
    [string]$ComposeEnvFile = "deploy/compose/capacity.env",
    [switch]$Rebuild
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$composeFile = Join-Path $projectRoot "docker-compose.yml"
$composeEnvPath = if ([IO.Path]::IsPathRooted($ComposeEnvFile)) { $ComposeEnvFile } else { Join-Path $projectRoot $ComposeEnvFile }
$composeArgs = @("compose", "--env-file", $composeEnvPath, "-f", $composeFile, "-p", $ComposeProject)

if ($Rebuild) {
    $buildArgs = $composeArgs + @("build", "backend", "worker")
    & docker @buildArgs
    if ($LASTEXITCODE -ne 0) { throw "Capacity application image build failed." }
}

$infraArgs = $composeArgs + @("up", "-d", "--wait", "postgres", "redis", "nats")
& docker @infraArgs
if ($LASTEXITCODE -ne 0) { throw "Capacity infrastructure startup failed." }

$migrateArgs = $composeArgs + @(
    "run", "--rm", "--no-deps",
    "--entrypoint", "/app/creatorinsight-migrate",
    "backend"
)
& docker @migrateArgs
if ($LASTEXITCODE -ne 0) { throw "Capacity database migration failed." }

$applicationArgs = $composeArgs + @("up", "-d", "--wait", "backend", "worker")
& docker @applicationArgs
if ($LASTEXITCODE -ne 0) { throw "Capacity application startup failed." }

Invoke-WebRequest -UseBasicParsing -TimeoutSec 15 "http://127.0.0.1:28080/ready" | Out-Null
Invoke-WebRequest -UseBasicParsing -TimeoutSec 15 "http://127.0.0.1:28081/ready" | Out-Null
Write-Host "Capacity stack is ready: API http://127.0.0.1:28080, worker http://127.0.0.1:28081."
