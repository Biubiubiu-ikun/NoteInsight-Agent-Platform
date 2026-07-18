param(
    [switch]$Build,
    [switch]$StartFrontend,
    [ValidateRange(1, 5)]
    [int]$WarmupAttempts = 3
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$ComposeFiles = @(
    "-f", (Join-Path $ProjectRoot "docker-compose.yml"),
    "-f", (Join-Path $ProjectRoot "deploy/observability/docker-compose.observability.yml"),
    "--profile", "retrieval"
)

Push-Location $ProjectRoot
try {
    docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) {
        docker desktop start
        if ($LASTEXITCODE -ne 0) { throw "Docker Desktop failed to start." }

        $dockerReady = $false
        for ($attempt = 0; $attempt -lt 120; $attempt++) {
            docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
            if ($LASTEXITCODE -eq 0) {
                $dockerReady = $true
                break
            }
            Start-Sleep -Seconds 2
        }
        if (-not $dockerReady) { throw "Docker engine did not become ready." }
    }

    if ($Build) {
        & (Join-Path $PSScriptRoot "build_backend_linux.ps1")
        if ($LASTEXITCODE -ne 0) { throw "Backend build failed." }
    }

    $upArguments = @("compose") + $ComposeFiles + @("up", "-d", "--wait")
    if ($Build) { $upArguments += "--build" }
    & docker @upArguments
    if ($LASTEXITCODE -ne 0) { throw "Local stack failed to start." }

    & (Join-Path $PSScriptRoot "migrate.ps1")
    if ($LASTEXITCODE -ne 0) { throw "Database migration failed." }

    foreach ($mode in @("lexical", "vector", "hybrid")) {
        $passed = $false
        for ($attempt = 1; $attempt -le $WarmupAttempts; $attempt++) {
            try {
                & (Join-Path $PSScriptRoot "smoke_phase7_retrieval.ps1") -Modes $mode
                $passed = $true
                break
            }
            catch {
                Write-Warning "Retrieval warm-up mode=$mode attempt=$attempt failed: $($_.Exception.Message)"
                if ($attempt -lt $WarmupAttempts) { Start-Sleep -Seconds 2 }
            }
        }
        if (-not $passed) { throw "Retrieval warm-up failed for mode=$mode." }
    }

    if ($StartFrontend) {
        & (Join-Path $PSScriptRoot "start_frontend.ps1")
    }

    Write-Host "Local stack is ready."
    Write-Host "API:      http://127.0.0.1:18080/ready"
    Write-Host "Worker:   http://127.0.0.1:18081/ready"
    Write-Host "Grafana:  http://127.0.0.1:13000/"
    Write-Host "Frontend: http://127.0.0.1:15173/"
}
finally {
    Pop-Location
}
