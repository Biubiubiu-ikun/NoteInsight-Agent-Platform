param(
    [string]$ResultRoot = "load-tests/results/phase6",
    [string]$Output = "load-tests/results/phase6/index.md"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$resultRootPath = Join-Path $projectRoot $ResultRoot
$outputPath = Join-Path $projectRoot $Output

function Get-MetricValue {
    param($Summary, [string]$MetricName, [string]$ValueName)
    $property = $Summary.metrics.PSObject.Properties[$MetricName]
    if ($null -eq $property) {
        return $null
    }
    $valueProperty = $property.Value.values.PSObject.Properties[$ValueName]
    if ($null -eq $valueProperty) {
        return $null
    }
    return [double]$valueProperty.Value
}

function Get-ThresholdResult {
    param($Summary)
    foreach ($metric in $Summary.metrics.PSObject.Properties) {
        if ($null -eq $metric.Value.thresholds) {
            continue
        }
        foreach ($threshold in $metric.Value.thresholds.PSObject.Properties) {
            if (-not [bool]$threshold.Value.ok) {
                return "FAIL"
            }
        }
    }
    return "PASS"
}

function Read-PrometheusCounters {
    param([string]$Path)
    $counters = @{}
    if (-not (Test-Path -LiteralPath $Path)) {
        return $counters
    }
    foreach ($line in Get-Content -LiteralPath $Path) {
        if ($line -match '^(cache_hit_total|cache_miss_total)\{cache="([^"]+)"\}\s+([0-9.eE+-]+)$') {
            $counters["$($Matches[1])/$($Matches[2])"] = [double]$Matches[3]
        }
    }
    return $counters
}

function Get-CacheHitRate {
    param([string]$RunPath)
    $before = Read-PrometheusCounters (Join-Path $RunPath "before\api-metrics.prom")
    $after = Read-PrometheusCounters (Join-Path $RunPath "after\api-metrics.prom")
    $hits = 0.0
    $misses = 0.0
    foreach ($key in $after.Keys) {
        $delta = $after[$key] - $(if ($before.ContainsKey($key)) { $before[$key] } else { 0 })
        if ($key.StartsWith("cache_hit_total/")) {
            $hits += $delta
        }
        elseif ($key.StartsWith("cache_miss_total/")) {
            $misses += $delta
        }
    }
    if ($hits + $misses -le 0) {
        return $null
    }
    return 100 * $hits / ($hits + $misses)
}

function Get-ResourcePeaks {
    param([string]$RunPath)
    $path = Join-Path $RunPath "docker-stats-timeseries.tsv"
    $peaks = @{}
    if (-not (Test-Path -LiteralPath $path)) {
        return $peaks
    }
    foreach ($line in Get-Content -LiteralPath $path) {
        $parts = $line -split "`t", 2
        if ($parts.Count -ne 2) {
            continue
        }
        try {
            $sample = $parts[1] | ConvertFrom-Json
            $cpu = [double]($sample.CPUPerc -replace '%', '')
            if (-not $peaks.ContainsKey($sample.Name) -or $cpu -gt $peaks[$sample.Name]) {
                $peaks[$sample.Name] = $cpu
            }
        }
        catch {
            continue
        }
    }
    return $peaks
}

function Format-Number {
    param($Value, [int]$Digits = 1, [string]$Suffix = "")
    if ($null -eq $Value) {
        return "n/a"
    }
    return "$([math]::Round([double]$Value, $Digits))$Suffix"
}

$runs = @()
foreach ($directory in Get-ChildItem -LiteralPath $resultRootPath -Directory | Sort-Object Name) {
    $summaryPath = Join-Path $directory.FullName "summary.json"
    $configPath = Join-Path $directory.FullName "run-config.json"
    if (-not (Test-Path -LiteralPath $summaryPath) -or -not (Test-Path -LiteralPath $configPath)) {
        continue
    }
    $summary = Get-Content -LiteralPath $summaryPath -Raw | ConvertFrom-Json
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $peaks = Get-ResourcePeaks $directory.FullName
    $containerPrefix = if ($null -ne $config.container_prefix -and $config.container_prefix) { $config.container_prefix } else { "creatorinsight" }
    $target = if ($config.profile -eq "step") { ($config.step_rps -join "->") } elseif ($config.profile -eq "spike") { "spike:$($config.spike_rps)" } else { [string]$config.rate }
    $runs += [pscustomobject]@{
        Run = $directory.Name
        Profile = $config.profile
        Workload = $config.workload
        Pattern = if ($null -ne $config.access_pattern) { $config.access_pattern } else { "uniform" }
        Target = $target
        Rps = Get-MetricValue $summary "http_reqs" "rate"
        P50 = Get-MetricValue $summary "http_req_duration" "med"
        P95 = Get-MetricValue $summary "http_req_duration" "p(95)"
        P99 = Get-MetricValue $summary "http_req_duration" "p(99)"
        ErrorRate = Get-MetricValue $summary "http_req_failed" "rate"
        Dropped = Get-MetricValue $summary "dropped_iterations" "count"
        CacheHit = Get-CacheHitRate $directory.FullName
        BackendCPU = $peaks["$containerPrefix-backend"]
        PostgresCPU = $peaks["$containerPrefix-postgres"]
        WorkerCPU = $peaks["$containerPrefix-worker"]
        Result = Get-ThresholdResult $summary
        Summary = $summary
    }
}

$lines = @(
    "# Phase 6 Capacity Run Index",
    "",
    "Generated at $((Get-Date).ToUniversalTime().ToString('o')). CPU percentages are Docker container peaks where time-series sampling was enabled.",
    "",
    "| Run | Profile | Workload | Pattern | Target RPS | Actual RPS | P50 ms | P95 ms | P99 ms | Error | Dropped | Cache hit | API CPU | PG CPU | Result |",
    "| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |"
)
foreach ($run in $runs) {
    $lines += "| $($run.Run) | $($run.Profile) | $($run.Workload) | $($run.Pattern) | $($run.Target) | $(Format-Number $run.Rps 2) | $(Format-Number $run.P50) | $(Format-Number $run.P95) | $(Format-Number $run.P99) | $(Format-Number (100 * $run.ErrorRate) 2 '%') | $(Format-Number $run.Dropped 0) | $(Format-Number $run.CacheHit 1 '%') | $(Format-Number $run.BackendCPU 1 '%') | $(Format-Number $run.PostgresCPU 1 '%') | $($run.Result) |"
}

$lines += @(
    "",
    "## Endpoint P95",
    "",
    "| Run | Notes list | Note detail | Comments | Rankings | Like | Collect | Share | Create comment | Comment like |",
    "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"
)
foreach ($run in $runs) {
    $s = $run.Summary
    $lines += "| $($run.Run) | $(Format-Number (Get-MetricValue $s 'notes_list_duration_ms' 'p(95)')) | $(Format-Number (Get-MetricValue $s 'note_detail_duration_ms' 'p(95)')) | $(Format-Number (Get-MetricValue $s 'comments_read_duration_ms' 'p(95)')) | $(Format-Number (Get-MetricValue $s 'rankings_read_duration_ms' 'p(95)')) | $(Format-Number (Get-MetricValue $s 'note_like_duration_ms' 'p(95)')) | $(Format-Number (Get-MetricValue $s 'note_collect_duration_ms' 'p(95)')) | $(Format-Number (Get-MetricValue $s 'note_share_duration_ms' 'p(95)')) | $(Format-Number (Get-MetricValue $s 'comment_create_duration_ms' 'p(95)')) | $(Format-Number (Get-MetricValue $s 'comment_like_duration_ms' 'p(95)')) |"
}

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $outputPath) | Out-Null
$lines -join "`n" | Set-Content -LiteralPath $outputPath -Encoding UTF8
Write-Output $outputPath
