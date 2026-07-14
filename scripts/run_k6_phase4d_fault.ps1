param(
    [string]$BaseUrl = "http://host.docker.internal:18080",
    [int]$Vus = 5,
    [int]$WarmupSeconds = 5,
    [int]$OutageSeconds = 10,
    [int]$RecoveryTrafficSeconds = 10,
    [int]$DrainTimeoutSeconds = 90,
    [string]$Image = "grafana/k6:latest"
)

$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $PSScriptRoot
$tokenFile = Join-Path $projectRoot "backend-go\tmp\dev_tokens.csv"
$natsContainer = "creatorinsight-nats"
$k6Container = "creatorinsight-k6-phase4d"
$durationSeconds = $WarmupSeconds + $OutageSeconds + $RecoveryTrafficSeconds

if (-not (Test-Path -LiteralPath $tokenFile)) {
    throw "Missing $tokenFile. Run seedgen with --with-tokens first."
}

docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) {
    docker pull $Image
}

$services = docker compose -f (Join-Path $projectRoot "docker-compose.yml") ps --status running --services
foreach ($required in @("backend", "worker", "postgres", "redis", "nats")) {
    if ($services -notcontains $required) {
        throw "Required service '$required' is not running."
    }
}

$mount = "${projectRoot}:/work"
$dockerArgs = @(
    "run", "--rm", "--name", $k6Container,
    "-v", $mount,
    "-w", "/work",
    "-e", "BASE_URL=$BaseUrl",
    "-e", "VUS=$Vus",
    "-e", "DURATION=${durationSeconds}s",
    $Image, "run", "load-tests/k6/phase4d_event_pipeline.js"
)
$dockerArgsJson = $dockerArgs | ConvertTo-Json -Compress

Write-Host "Starting Phase 4D event traffic for ${durationSeconds}s."
$k6Job = Start-Job -ScriptBlock {
    param([string]$DockerArgsJson)
    $arguments = $DockerArgsJson | ConvertFrom-Json
    & docker @arguments
    if ($LASTEXITCODE -ne 0) {
        throw "k6 docker process exited with code $LASTEXITCODE."
    }
} -ArgumentList $dockerArgsJson

try {
    Start-Sleep -Seconds $WarmupSeconds
    Write-Host "Stopping NATS for ${OutageSeconds}s. API writes must continue through PostgreSQL Outbox."
    docker stop $natsContainer | Out-Null
    Start-Sleep -Seconds $OutageSeconds
    Write-Host "Restarting NATS and waiting for automatic recovery."
    docker start $natsContainer | Out-Null

    Wait-Job -Job $k6Job | Out-Null
    Receive-Job -Job $k6Job
    if ($k6Job.State -ne "Completed") {
        throw "k6 job ended in state $($k6Job.State): $($k6Job.JobStateInfo.Reason)"
    }

    $deadline = (Get-Date).AddSeconds($DrainTimeoutSeconds)
    $drained = $false
    do {
        $activeOutbox = [int](docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -Atc `
            "SELECT COUNT(*) FROM outbox_events WHERE status IN ('pending','processing','retry');")
        $metrics = (Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:18081/metrics").Content
        $pendingMatch = [regex]::Match($metrics, '(?m)^jetstream_consumer_pending_messages\s+([0-9.eE+-]+)$')
        $ackMatch = [regex]::Match($metrics, '(?m)^jetstream_consumer_ack_pending_messages\s+([0-9.eE+-]+)$')
        $connectedMatch = [regex]::Match($metrics, '(?m)^nats_connected\s+([0-9.eE+-]+)$')
        $pending = if ($pendingMatch.Success) { [double]$pendingMatch.Groups[1].Value } else { -1 }
        $ackPending = if ($ackMatch.Success) { [double]$ackMatch.Groups[1].Value } else { -1 }
        $connected = if ($connectedMatch.Success) { [double]$connectedMatch.Groups[1].Value } else { 0 }
        $drained = $activeOutbox -eq 0 -and $pending -eq 0 -and $ackPending -eq 0 -and $connected -eq 1
        if (-not $drained) {
            Start-Sleep -Seconds 2
        }
    } while (-not $drained -and (Get-Date) -lt $deadline)

    if (-not $drained) {
        throw "Event pipeline did not drain: outbox=$activeOutbox pending=$pending ack_pending=$ackPending connected=$connected"
    }

    $lagCount = [regex]::Match($metrics, '(?m)^domain_event_lag_seconds_count\{event_type="note.shared"\}\s+([0-9.eE+-]+)$')
    Write-Host "Phase 4D recovery passed: outbox=0, JetStream pending=0, ack_pending=0."
    if ($lagCount.Success) {
        Write-Host "Observed note.shared lag samples: $($lagCount.Groups[1].Value)"
    }
}
finally {
    $natsRunning = docker inspect -f "{{.State.Running}}" $natsContainer 2>$null
    if ($natsRunning -ne "true") {
        docker start $natsContainer | Out-Null
    }
    if ($k6Job.State -eq "Running") {
        Stop-Job -Job $k6Job
        docker rm -f $k6Container *> $null
    }
    Remove-Job -Job $k6Job -Force -ErrorAction SilentlyContinue
}
