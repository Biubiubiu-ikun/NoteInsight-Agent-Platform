param(
    [ValidateSet("baseline", "step", "spike", "soak")]
    [string]$Profile = "baseline",
    [ValidateSet("lexical", "vector", "hybrid", "mixed")]
    [string]$Mode = "mixed",
    [ValidateSet("none", "qdrant_restart", "tei_restart")]
    [string]$Fault = "none",
    [string]$BaseUrl = "http://host.docker.internal:18080",
    [int]$Rate = 2,
    [string]$Duration = "30s",
    [int]$StepLowRps = 2,
    [int]$StepMidRps = 4,
    [int]$StepHighRps = 6,
    [int]$SpikeRps = 10,
    [int]$PreallocatedVus = 20,
    [int]$MaxVus = 100,
    [int]$ProjectId = 1,
    [int]$DatasetVersionId = 2,
    [string]$IngestionRunId = "phase7a_dv2_rebuild_v2_20260718",
    [string]$BenchmarkFile = "evaluation/benchmarks/retrieval_v4/development.jsonl",
    [string]$TokenFile = "backend-go/tmp/dev_tokens.csv",
    [string]$ConcurrentIndexIngestionRunId = "",
    [int]$ConcurrentIndexBatchSize = 8,
    [int]$FaultAfterSeconds = 10,
    [switch]$AllowRateLimitSaturation,
    [string]$ResultRoot = "load-tests/results/phase7",
    [string]$ContainerPrefix = "creatorinsight",
    [string]$Image = "grafana/k6@sha256:e7eeddf1ce2361df6920d925297f487c0ba549c44be242c6a9c22f28d9b08efa"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$benchmarkPath = Join-Path $projectRoot $BenchmarkFile
$tokenPath = Join-Path $projectRoot $TokenFile
if (-not (Test-Path -LiteralPath $benchmarkPath)) {
    throw "Missing public development benchmark $benchmarkPath"
}
if ($ProjectId -le 0 -or $DatasetVersionId -le 0 -or $Rate -le 0 -or $ConcurrentIndexBatchSize -le 0) {
    throw "ProjectId, DatasetVersionId, Rate, and ConcurrentIndexBatchSize must be positive."
}

$runID = "{0}-{1}-{2}-{3}" -f (Get-Date -Format "yyyyMMdd-HHmmss"), $Profile, $Mode, $Fault
$resultDir = Join-Path (Join-Path $projectRoot $ResultRoot) $runID
New-Item -ItemType Directory -Force -Path $resultDir | Out-Null
$containers = @(
    "$ContainerPrefix-backend",
    "$ContainerPrefix-postgres",
    "$ContainerPrefix-qdrant",
    "$ContainerPrefix-text-embeddings"
)
$requiredServices = @("backend", "postgres", "qdrant", "text-embeddings")
$runningServices = docker compose --profile retrieval ps --status running --services
foreach ($service in $requiredServices) {
    if ($runningServices -notcontains $service) {
        throw "Required retrieval service '$service' is not running."
    }
}

$backendEnvironment = docker inspect "$ContainerPrefix-backend" --format "{{range .Config.Env}}{{println .}}{{end}}"
$rateLimitLine = $backendEnvironment | Where-Object { $_ -like "RATE_LIMIT_RETRIEVAL_READ_LIMIT=*" } | Select-Object -First 1
$retrievalRateLimit = if ($rateLimitLine) { [int](($rateLimitLine -split "=", 2)[1]) } else { 120 }
$maximumPlannedRps = switch ($Profile) {
    "step" { $StepHighRps }
    "spike" { $SpikeRps }
    default { $Rate }
}
$minimumCapacityLimit = [int][Math]::Ceiling($maximumPlannedRps * 60 * 1.2)
if (-not $AllowRateLimitSaturation -and $retrievalRateLimit -lt $minimumCapacityLimit) {
    throw "Backend retrieval rate limit is $retrievalRateLimit/min, below the capacity-test minimum $minimumCapacityLimit/min. Recreate the backend with a higher RATE_LIMIT_RETRIEVAL_READ_LIMIT or pass -AllowRateLimitSaturation for an intentional limiter test."
}

docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) {
    docker pull $Image
    if ($LASTEXITCODE -ne 0) { throw "Unable to pull k6 image $Image" }
}

$runConfig = [ordered]@{
    run_id = $runID
    started_at = (Get-Date).ToUniversalTime().ToString("o")
    profile = $Profile
    mode = $Mode
    fault = $Fault
    rate = $Rate
    duration = $Duration
    step_rps = @($StepLowRps, $StepMidRps, $StepHighRps)
    spike_rps = $SpikeRps
    project_id = $ProjectId
    dataset_version_id = $DatasetVersionId
    ingestion_run_id = $IngestionRunId
    benchmark_file = $BenchmarkFile
    benchmark_sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $benchmarkPath).Hash.ToLowerInvariant()
    token_file_present = (Test-Path -LiteralPath $tokenPath)
    concurrent_index_ingestion_run_id = $ConcurrentIndexIngestionRunId
    concurrent_index_batch_size = if ($ConcurrentIndexIngestionRunId) { $ConcurrentIndexBatchSize } else { 0 }
    k6_image = $Image
    application_retrieval_rate_limit_per_minute = $retrievalRateLimit
    maximum_planned_rps = $maximumPlannedRps
}

function Save-WebContent {
    param([string]$Url, [string]$Path)
    try {
        (Invoke-WebRequest -UseBasicParsing -TimeoutSec 20 $Url).Content |
            Set-Content -LiteralPath $Path -Encoding UTF8
    }
    catch {
        "snapshot_error=$($_.Exception.Message)" | Set-Content -LiteralPath $Path -Encoding UTF8
    }
}

function Save-Snapshot {
    param([string]$Name)
    $snapshotDir = Join-Path $resultDir $Name
    New-Item -ItemType Directory -Force -Path $snapshotDir | Out-Null
    docker stats --no-stream --format "{{json .}}" @containers 2>&1 |
        Set-Content -LiteralPath (Join-Path $snapshotDir "docker-stats.jsonl") -Encoding UTF8
    docker compose --profile retrieval ps 2>&1 |
        Set-Content -LiteralPath (Join-Path $snapshotDir "compose-ps.txt") -Encoding UTF8
    Save-WebContent "http://127.0.0.1:18080/metrics" (Join-Path $snapshotDir "api-metrics.prom")
    Save-WebContent "http://127.0.0.1:16333/metrics" (Join-Path $snapshotDir "qdrant-metrics.prom")
    Save-WebContent "http://127.0.0.1:18082/metrics" (Join-Path $snapshotDir "tei-metrics.prom")
    Save-WebContent "http://127.0.0.1:18080/ready" (Join-Path $snapshotDir "api-ready.json")
    try {
        docker exec "$ContainerPrefix-text-embeddings" nvidia-smi `
            --query-gpu=timestamp,name,utilization.gpu,utilization.memory,memory.used,memory.total `
            --format=csv,noheader,nounits 2>&1 |
            Set-Content -LiteralPath (Join-Path $snapshotDir "gpu.csv") -Encoding UTF8
    }
    catch {
        "gpu_snapshot_error=$($_.Exception.Message)" |
            Set-Content -LiteralPath (Join-Path $snapshotDir "gpu.csv") -Encoding UTF8
    }
}

function Start-ResourceMonitor {
    $dockerPath = Join-Path $resultDir "docker-stats-timeseries.tsv"
    $gpuPath = Join-Path $resultDir "gpu-timeseries.csv"
    $containerJson = $containers | ConvertTo-Json -Compress
    return Start-Job -ScriptBlock {
        param([string]$DockerPath, [string]$GPUPath, [string]$ContainerJson, [string]$TEIContainer)
        $monitoredContainers = $ContainerJson | ConvertFrom-Json
        while ($true) {
            $timestamp = (Get-Date).ToUniversalTime().ToString("o")
            foreach ($line in (& docker stats --no-stream --format "{{json .}}" @monitoredContainers 2>&1)) {
                "$timestamp`t$line" | Add-Content -LiteralPath $DockerPath -Encoding UTF8
            }
            $gpu = & docker exec $TEIContainer nvidia-smi `
                --query-gpu=utilization.gpu,utilization.memory,memory.used,memory.total `
                --format=csv,noheader,nounits 2>&1
            "$timestamp,$gpu" | Add-Content -LiteralPath $GPUPath -Encoding UTF8
            Start-Sleep -Seconds 2
        }
    } -ArgumentList $dockerPath, $gpuPath, $containerJson, "$ContainerPrefix-text-embeddings"
}

function Wait-RetrievalReady {
    param([int]$TimeoutSeconds = 240)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        try {
            Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 "http://127.0.0.1:18080/ready" | Out-Null
            Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 "http://127.0.0.1:16333/readyz" | Out-Null
            Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 "http://127.0.0.1:18082/health" | Out-Null
            return $true
        }
        catch {
            Start-Sleep -Seconds 2
        }
    } while ((Get-Date) -lt $deadline)
    return $false
}

function Test-RecoveryQuery {
    $hostBase = $BaseUrl.Replace("host.docker.internal", "127.0.0.1").TrimEnd("/")
    $body = @{
        project_id = $ProjectId
        dataset_version_id = $DatasetVersionId
        ingestion_run_id = $IngestionRunId
        query = "敏感肌通勤防晒的主要风险是什么？"
        mode = if ($Mode -eq "mixed") { "hybrid" } else { $Mode }
        limit = 5
    } | ConvertTo-Json -Compress
    $deadline = (Get-Date).AddSeconds(45)
    do {
        try {
            $response = Invoke-RestMethod -Method Post -Uri "$hostBase/api/v1/retrieval/search" `
                -ContentType "application/json" -Body $body -TimeoutSec 15
            if ($response.scope.ingestion_run_id -ne $IngestionRunId) {
                throw "Recovery query returned a different ingestion run."
            }
            return
        }
        catch {
            $statusCode = 0
            if ($null -ne $_.Exception.Response) {
                $statusCode = [int]$_.Exception.Response.StatusCode
            }
            if ($statusCode -ne 429 -or (Get-Date) -ge $deadline) { throw }
            Start-Sleep -Seconds 2
        }
    } while ((Get-Date) -lt $deadline)
    if ((Get-Date) -ge $deadline) {
        throw "Recovery query remained rate limited for 45 seconds."
    }
}

$indexContainer = "phase7d-vector-$($runID -replace '[^A-Za-z0-9_.-]', '-')"
$indexStarted = $false
$faultJob = $null
$monitorJob = $null
$k6ExitCode = 1
$recovered = $false
$runConfig | ConvertTo-Json -Depth 6 |
    Set-Content -LiteralPath (Join-Path $resultDir "run-config.json") -Encoding UTF8

try {
    Save-Snapshot "before"
    $monitorJob = Start-ResourceMonitor
    if ($ConcurrentIndexIngestionRunId) {
        docker compose --profile retrieval run -d --name $indexContainer --no-deps `
            -e "EMBEDDING_BATCH_SIZE=$ConcurrentIndexBatchSize" `
            --entrypoint /app/noteinsight-vector-index backend `
            --ingestion-run-id $ConcurrentIndexIngestionRunId --timeout 6h | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "Failed to start concurrent vector index container." }
        $indexStarted = $true
    }
    if ($Fault -ne "none") {
        $faultContainer = if ($Fault -eq "qdrant_restart") { "$ContainerPrefix-qdrant" } else { "$ContainerPrefix-text-embeddings" }
        $faultLog = Join-Path $resultDir "fault-events.log"
        $faultJob = Start-Job -ScriptBlock {
            param([int]$Delay, [string]$Container, [string]$Path)
            Start-Sleep -Seconds $Delay
            "$(Get-Date -Format o) restart_begin $Container" | Add-Content -LiteralPath $Path
            & docker restart $Container | Out-Null
            "$(Get-Date -Format o) restart_complete $Container" | Add-Content -LiteralPath $Path
        } -ArgumentList $FaultAfterSeconds, $faultContainer, $faultLog
    }

    $benchmarkContainerPath = "/work/" + $BenchmarkFile.Replace("\", "/")
    $tokenContainerPath = "/work/" + $TokenFile.Replace("\", "/")
    $maxErrorRate = if ($Fault -eq "none") { "0.02" } else { "0.35" }
    $minCheckRate = if ($Fault -eq "none") { "0.98" } else { "0.65" }
    $dockerArgs = @(
        "run", "--rm",
        "-v", "${projectRoot}:/work",
        "-v", "${resultDir}:/results",
        "-w", "/work",
        "-e", "K6_NO_USAGE_REPORT=true",
        "-e", "BASE_URL=$BaseUrl",
        "-e", "PROFILE=$Profile",
        "-e", "MODE=$Mode",
        "-e", "RATE=$Rate",
        "-e", "DURATION=$Duration",
        "-e", "STEP_LOW_RPS=$StepLowRps",
        "-e", "STEP_MID_RPS=$StepMidRps",
        "-e", "STEP_HIGH_RPS=$StepHighRps",
        "-e", "SPIKE_RPS=$SpikeRps",
        "-e", "PREALLOCATED_VUS=$PreallocatedVus",
        "-e", "MAX_VUS=$MaxVus",
        "-e", "PROJECT_ID=$ProjectId",
        "-e", "DATASET_VERSION_ID=$DatasetVersionId",
        "-e", "INGESTION_RUN_ID=$IngestionRunId",
        "-e", "BENCHMARK_FILE=$benchmarkContainerPath",
        "-e", "TOKEN_FILE=$tokenContainerPath",
        "-e", "MAX_ERROR_RATE=$maxErrorRate",
        "-e", "MAX_TIMEOUT_RATE=$maxErrorRate",
        "-e", "MAX_RATE_LIMIT_RATE=0.001",
        "-e", "MIN_CHECK_RATE=$minCheckRate",
        $Image, "run", "--summary-export", "/results/summary-export.json",
        "load-tests/k6/phase7_retrieval.js"
    )
    $previousErrorAction = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        & docker @dockerArgs 2>&1 | ForEach-Object { $_.ToString() } |
            Tee-Object -FilePath (Join-Path $resultDir "k6-console.txt")
        $k6ExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorAction
    }
    if ($null -ne $faultJob) {
        Wait-Job -Job $faultJob -Timeout 300 | Out-Null
        Receive-Job -Job $faultJob -ErrorAction SilentlyContinue | Out-Null
    }
    $recovered = Wait-RetrievalReady
    if ($recovered) { Test-RecoveryQuery }
}
finally {
    if ($null -ne $monitorJob) {
        Stop-Job -Job $monitorJob -ErrorAction SilentlyContinue
        Receive-Job -Job $monitorJob -ErrorAction SilentlyContinue | Out-Null
        Remove-Job -Job $monitorJob -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $faultJob) {
        Stop-Job -Job $faultJob -ErrorAction SilentlyContinue
        Remove-Job -Job $faultJob -Force -ErrorAction SilentlyContinue
    }
    if ($indexStarted) {
        docker logs $indexContainer 2>&1 | Set-Content -LiteralPath (Join-Path $resultDir "vector-index.log") -Encoding UTF8
        $running = docker inspect $indexContainer --format "{{.State.Running}}" 2>$null
        if ($running -eq "true") { docker stop -t 20 $indexContainer | Out-Null }
        docker inspect $indexContainer --format "{{json .State}}" 2>&1 |
            Set-Content -LiteralPath (Join-Path $resultDir "vector-index-state.json") -Encoding UTF8
        docker rm $indexContainer | Out-Null
    }
    Save-Snapshot "after"
    $runConfig.finished_at = (Get-Date).ToUniversalTime().ToString("o")
    $runConfig.k6_exit_code = $k6ExitCode
    $runConfig.dependencies_recovered = $recovered
    $runConfig | ConvertTo-Json -Depth 6 |
        Set-Content -LiteralPath (Join-Path $resultDir "run-config.json") -Encoding UTF8
}

if (-not $recovered) { throw "Retrieval dependencies did not recover within 240 seconds. Inspect $resultDir" }
if ($k6ExitCode -ne 0) { throw "k6 thresholds failed with exit code $k6ExitCode. Inspect $resultDir" }
Write-Host "Phase 7 retrieval load passed. Results: $resultDir"
