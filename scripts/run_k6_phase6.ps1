param(
    [ValidateSet("baseline", "step", "spike", "soak")]
    [string]$Profile = "baseline",
    [ValidateSet("mixed", "notes_list", "note_detail", "comments_read", "rankings_read", "writes")]
    [string]$Workload = "mixed",
    [ValidateSet("uniform", "hotspot")]
    [string]$AccessPattern = "uniform",
    [string]$BaseUrl = "http://host.docker.internal:18080",
    [int]$Rate = 25,
    [string]$Duration = "45s",
    [int]$StepLowRps = 25,
    [int]$StepMidRps = 50,
    [int]$StepHighRps = 75,
    [int]$SpikeRps = 120,
    [int]$PreallocatedVus = 40,
    [int]$MaxVus = 200,
    [int]$NoteStart = 1,
    [int]$NoteCount = 5000,
    [int]$CommentStart = 1,
    [int]$CommentCount = 20000,
    [int]$HotNoteCount = 100,
    [string]$ResultRoot = "load-tests/results/phase6",
    [string]$Image = "grafana/k6:latest"
)

$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $PSScriptRoot
$tokenFile = Join-Path $projectRoot "backend-go\tmp\dev_tokens.csv"
$composeFile = Join-Path $projectRoot "docker-compose.yml"
$runID = "{0}-{1}-{2}" -f (Get-Date -Format "yyyyMMdd-HHmmss"), $Profile, $Workload
$resultRootPath = Join-Path $projectRoot $ResultRoot
$resultDir = Join-Path $resultRootPath $runID
$requiredServices = @("backend", "worker", "postgres", "redis", "nats")
$containers = @("creatorinsight-backend", "creatorinsight-worker", "creatorinsight-postgres", "creatorinsight-redis", "creatorinsight-nats")

if (($Workload -eq "mixed" -or $Workload -eq "writes") -and -not (Test-Path -LiteralPath $tokenFile)) {
    throw "Missing $tokenFile. Run seedgen with --with-tokens first."
}
if ($NoteCount -le 0 -or $CommentCount -le 0) {
    throw "NoteCount and CommentCount must be positive."
}

$services = docker compose -f $composeFile ps --status running --services
foreach ($required in $requiredServices) {
    if ($services -notcontains $required) {
        throw "Required service '$required' is not running."
    }
}

docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) {
    docker pull $Image
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to pull k6 image $Image."
    }
}

New-Item -ItemType Directory -Force -Path $resultDir | Out-Null

$runConfig = [ordered]@{
    run_id = $runID
    started_at = (Get-Date).ToUniversalTime().ToString("o")
    profile = $Profile
    workload = $Workload
    access_pattern = $AccessPattern
    base_url = $BaseUrl
    rate = $Rate
    duration = $Duration
    step_rps = @($StepLowRps, $StepMidRps, $StepHighRps)
    spike_rps = $SpikeRps
    preallocated_vus = $PreallocatedVus
    max_vus = $MaxVus
    note_range = @{ start = $NoteStart; count = $NoteCount }
    comment_range = @{ start = $CommentStart; count = $CommentCount }
    hot_note_count = $HotNoteCount
    k6_image = $Image
}
$runConfig | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $resultDir "run-config.json") -Encoding UTF8

function Save-WebContent {
    param([string]$Url, [string]$Path)
    try {
        (Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 $Url).Content | Set-Content -LiteralPath $Path -Encoding UTF8
    }
    catch {
        "snapshot_error=$($_.Exception.Message)" | Set-Content -LiteralPath $Path -Encoding UTF8
    }
}

function Save-Snapshot {
    param([string]$Name)

    $snapshotDir = Join-Path $resultDir $Name
    New-Item -ItemType Directory -Force -Path $snapshotDir | Out-Null

    docker version --format "{{json .Server}}" 2>&1 | Set-Content -LiteralPath (Join-Path $snapshotDir "docker-version.json") -Encoding UTF8
    docker info --format "{{json .}}" 2>&1 | Set-Content -LiteralPath (Join-Path $snapshotDir "docker-info.json") -Encoding UTF8
    docker stats --no-stream --format "{{json .}}" @containers 2>&1 | Set-Content -LiteralPath (Join-Path $snapshotDir "docker-stats.jsonl") -Encoding UTF8
    docker compose -f $composeFile ps 2>&1 | Set-Content -LiteralPath (Join-Path $snapshotDir "compose-ps.txt") -Encoding UTF8

    $dataQuery = @"
SELECT 'users', COUNT(*) FROM users
UNION ALL SELECT 'notes', COUNT(*) FROM notes
UNION ALL SELECT 'published_notes', COUNT(*) FROM notes WHERE status = 'published'
UNION ALL SELECT 'note_media', COUNT(*) FROM note_media
UNION ALL SELECT 'note_comments', COUNT(*) FROM note_comments
UNION ALL SELECT 'note_likes', COUNT(*) FROM note_likes
UNION ALL SELECT 'note_collects', COUNT(*) FROM note_collects
UNION ALL SELECT 'note_shares', COUNT(*) FROM note_shares
UNION ALL SELECT 'note_comment_likes', COUNT(*) FROM note_comment_likes
UNION ALL SELECT 'behavior_events', COUNT(*) FROM behavior_events
UNION ALL SELECT 'outbox_active', COUNT(*) FROM outbox_events WHERE status IN ('pending','processing','retry')
UNION ALL SELECT 'outbox_failed', COUNT(*) FROM outbox_events WHERE status = 'failed';
"@
    docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -At -F "," -c $dataQuery 2>&1 |
        Set-Content -LiteralPath (Join-Path $snapshotDir "postgres-counts.csv") -Encoding UTF8

    $activityQuery = @"
SELECT datname, numbackends, xact_commit, xact_rollback, blks_read, blks_hit, tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted, temp_files, temp_bytes, deadlocks
FROM pg_stat_database WHERE datname = 'creatorinsight';
"@
    docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -P footer=off -A -F "," -c $activityQuery 2>&1 |
        Set-Content -LiteralPath (Join-Path $snapshotDir "postgres-activity.csv") -Encoding UTF8

    docker exec creatorinsight-redis redis-cli INFO stats memory keyspace 2>&1 |
        Set-Content -LiteralPath (Join-Path $snapshotDir "redis-info.txt") -Encoding UTF8
    Save-WebContent -Url "http://127.0.0.1:18080/metrics" -Path (Join-Path $snapshotDir "api-metrics.prom")
    Save-WebContent -Url "http://127.0.0.1:18081/metrics" -Path (Join-Path $snapshotDir "worker-metrics.prom")
    Save-WebContent -Url "http://127.0.0.1:18222/varz" -Path (Join-Path $snapshotDir "nats-varz.json")
    Save-WebContent -Url "http://127.0.0.1:18222/jsz?consumers=true" -Path (Join-Path $snapshotDir "nats-jsz.json")
}

function Wait-EventPipelineDrain {
    param([int]$TimeoutSeconds = 90)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $active = [int](docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -Atc "SELECT COUNT(*) FROM outbox_events WHERE status IN ('pending','processing','retry');")
        $metrics = (Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 "http://127.0.0.1:18081/metrics").Content
        $pendingMatch = [regex]::Match($metrics, '(?m)^jetstream_consumer_pending_messages\s+([0-9.eE+-]+)$')
        $ackMatch = [regex]::Match($metrics, '(?m)^jetstream_consumer_ack_pending_messages\s+([0-9.eE+-]+)$')
        $pending = if ($pendingMatch.Success) { [double]$pendingMatch.Groups[1].Value } else { -1 }
        $ackPending = if ($ackMatch.Success) { [double]$ackMatch.Groups[1].Value } else { -1 }
        if ($active -eq 0 -and $pending -eq 0 -and $ackPending -eq 0) {
            return $true
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    return $false
}

function Start-ResourceMonitor {
    $monitorPath = Join-Path $resultDir "docker-stats-timeseries.tsv"
    $containerNames = $containers | ConvertTo-Json -Compress
    return Start-Job -ScriptBlock {
        param([string]$Path, [string]$ContainerNames)
        $names = $ContainerNames | ConvertFrom-Json
        while ($true) {
            $timestamp = (Get-Date).ToUniversalTime().ToString("o")
            $stats = & docker stats --no-stream --format "{{json .}}" @names 2>&1
            foreach ($line in $stats) {
                "$timestamp`t$line" | Add-Content -LiteralPath $Path -Encoding UTF8
            }
            Start-Sleep -Seconds 2
        }
    } -ArgumentList $monitorPath, $containerNames
}

function Stop-ResourceMonitor {
    param($Job)
    if ($null -eq $Job) {
        return
    }
    Stop-Job -Job $Job -ErrorAction SilentlyContinue
    Receive-Job -Job $Job -ErrorAction SilentlyContinue | Out-Null
    Remove-Job -Job $Job -Force -ErrorAction SilentlyContinue
}

function Warm-Hotset {
    if ($AccessPattern -ne "hotspot") {
        return
    }
    $hostBaseUrl = $BaseUrl.Replace("host.docker.internal", "127.0.0.1").TrimEnd("/")
    Write-Host "Prewarming $HotNoteCount hot notes before metrics snapshot."
    for ($offset = 0; $offset -lt $HotNoteCount; $offset++) {
        $noteID = $NoteStart + $offset
        if ($Workload -eq "mixed" -or $Workload -eq "note_detail") {
            Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 "$hostBaseUrl/api/v1/notes/$noteID" | Out-Null
        }
        if ($Workload -eq "mixed" -or $Workload -eq "comments_read") {
            Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 "$hostBaseUrl/api/v1/notes/$noteID/comments?limit=20" | Out-Null
        }
    }
}

Warm-Hotset
Save-Snapshot -Name "before"
$monitorJob = Start-ResourceMonitor

$mount = "${projectRoot}:/work"
$resultMount = "${resultDir}:/results"
$dockerArgs = @(
    "run", "--rm",
    "-v", $mount,
    "-v", $resultMount,
    "-w", "/work",
    "-e", "K6_NO_USAGE_REPORT=true",
    "-e", "BASE_URL=$BaseUrl",
    "-e", "TOKEN_FILE=/work/backend-go/tmp/dev_tokens.csv",
    "-e", "PROFILE=$Profile",
    "-e", "WORKLOAD=$Workload",
    "-e", "ACCESS_PATTERN=$AccessPattern",
    "-e", "RATE=$Rate",
    "-e", "DURATION=$Duration",
    "-e", "STEP_LOW_RPS=$StepLowRps",
    "-e", "STEP_MID_RPS=$StepMidRps",
    "-e", "STEP_HIGH_RPS=$StepHighRps",
    "-e", "SPIKE_RPS=$SpikeRps",
    "-e", "PREALLOCATED_VUS=$PreallocatedVus",
    "-e", "MAX_VUS=$MaxVus",
    "-e", "NOTE_START=$NoteStart",
    "-e", "NOTE_COUNT=$NoteCount",
    "-e", "COMMENT_START=$CommentStart",
    "-e", "COMMENT_COUNT=$CommentCount",
    "-e", "HOT_NOTE_COUNT=$HotNoteCount",
    $Image, "run", "--summary-export", "/results/summary-export.json", "load-tests/k6/phase6_capacity.js"
)

$k6ExitCode = 1
$drained = $false
try {
    Write-Host "Running Phase 6 $Profile/$Workload. Results: $resultDir"
    $previousErrorAction = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
		$consolePath = Join-Path $resultDir "k6-console.txt"
        & docker @dockerArgs 2>&1 | ForEach-Object { $_.ToString() } | Tee-Object -FilePath $consolePath
        $k6ExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorAction
    }
    $drained = Wait-EventPipelineDrain
}
finally {
    Stop-ResourceMonitor -Job $monitorJob
    Save-Snapshot -Name "after"
    $runConfig.finished_at = (Get-Date).ToUniversalTime().ToString("o")
    $runConfig.k6_exit_code = $k6ExitCode
    $runConfig.event_pipeline_drained = $drained
    $runConfig | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $resultDir "run-config.json") -Encoding UTF8
}

if (-not $drained) {
    throw "Event pipeline did not drain within 90 seconds. Inspect $resultDir."
}
if ($k6ExitCode -ne 0) {
    throw "k6 thresholds failed with exit code $k6ExitCode. Inspect $resultDir."
}

Write-Host "Phase 6 run passed. Summary: $(Join-Path $resultDir 'summary.md')"
