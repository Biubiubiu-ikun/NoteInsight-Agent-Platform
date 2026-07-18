param(
    [ValidateSet("create", "list", "restore-drill", "prune")]
    [string]$Operation = "list",
    [Parameter(Mandatory = $true)]
    [string]$Collection,
    [string]$SnapshotName = "",
    [string]$BaseUrl = "http://127.0.0.1:16333",
    [string]$ApiKey = "",
    [string]$OutputDirectory = "artifacts/qdrant-snapshots",
    [ValidateRange(1, 100)]
    [int]$RetainCount = 3,
    [switch]$KeepRestoredCollection
)

$ErrorActionPreference = "Stop"
$base = $BaseUrl.TrimEnd("/")
$encodedCollection = [Uri]::EscapeDataString($Collection)
$headers = @{}
if ($ApiKey) {
    $headers["api-key"] = $ApiKey
}

function Invoke-QdrantJson {
    param(
        [string]$Method,
        [string]$Path,
        [object]$Body = $null
    )
    $arguments = @{
        Method = $Method
        Uri = "$base$Path"
        Headers = $headers
        TimeoutSec = 1800
    }
    if ($null -ne $Body) {
        $arguments.ContentType = "application/json"
        $arguments.Body = $Body | ConvertTo-Json -Depth 8 -Compress
    }
    Invoke-RestMethod @arguments
}

function New-QdrantSnapshot {
    $response = Invoke-QdrantJson -Method Post -Path "/collections/$encodedCollection/snapshots?wait=true"
    if (-not $response.result.name) {
        throw "Qdrant did not return a snapshot name"
    }
    $response.result
}

function Save-QdrantSnapshot {
    param([string]$Name)
    $targetDirectory = Join-Path $OutputDirectory $Collection
    New-Item -ItemType Directory -Force -Path $targetDirectory | Out-Null
    $targetPath = Join-Path $targetDirectory $Name
    $curlArguments = @(
        "--fail",
        "--silent",
        "--show-error",
        "--location",
        "--output", $targetPath
    )
    if ($ApiKey) {
        $curlArguments += @("--header", "api-key: $ApiKey")
    }
    $curlArguments += "$base/collections/$encodedCollection/snapshots/$([Uri]::EscapeDataString($Name))"
    & curl.exe @curlArguments
    if ($LASTEXITCODE -ne 0) {
        throw "curl failed to download Qdrant snapshot (exit $LASTEXITCODE)"
    }
    $file = Get-Item -LiteralPath $targetPath
    [pscustomobject]@{
        path = $file.FullName
        bytes = $file.Length
        sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash.ToLowerInvariant()
    }
}

function Get-QdrantPointCount {
    param([string]$Name)
    $encodedName = [Uri]::EscapeDataString($Name)
    $response = Invoke-QdrantJson -Method Post -Path "/collections/$encodedName/points/count" -Body @{ exact = $true }
    [long]$response.result.count
}

switch ($Operation) {
    "list" {
        Invoke-QdrantJson -Method Get -Path "/collections/$encodedCollection/snapshots"
        break
    }
    "create" {
        $snapshot = New-QdrantSnapshot
        $artifact = Save-QdrantSnapshot -Name $snapshot.name
        [pscustomobject]@{
            collection = $Collection
            snapshot_name = $snapshot.name
            created_at = $snapshot.creation_time
            qdrant_size_bytes = $snapshot.size
            artifact = $artifact
        }
        break
    }
    "restore-drill" {
        if (-not $SnapshotName) {
            $snapshot = New-QdrantSnapshot
            $SnapshotName = $snapshot.name
        }
        $artifact = Save-QdrantSnapshot -Name $SnapshotName
        $restoreCollection = "${Collection}_restore_$([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())"
        $encodedRestoreCollection = [Uri]::EscapeDataString($restoreCollection)
        $snapshotLocation = "file:///qdrant/snapshots/$Collection/$SnapshotName"
        $sourceCount = Get-QdrantPointCount -Name $Collection
        try {
            Invoke-QdrantJson `
                -Method Put `
                -Path "/collections/$encodedRestoreCollection/snapshots/recover?wait=true" `
                -Body @{ location = $snapshotLocation; priority = "snapshot" } | Out-Null
            $restoredCount = Get-QdrantPointCount -Name $restoreCollection
            if ($restoredCount -ne $sourceCount) {
                throw "restored point count $restoredCount does not match source $sourceCount"
            }
            [pscustomobject]@{
                source_collection = $Collection
                restored_collection = $restoreCollection
                snapshot_name = $SnapshotName
                point_count = $restoredCount
                artifact = $artifact
                verified = $true
            }
        }
        finally {
            if (-not $KeepRestoredCollection) {
                try {
                    Invoke-QdrantJson -Method Delete -Path "/collections/$encodedRestoreCollection" | Out-Null
                }
                catch {
                    Write-Warning "failed to remove restore drill collection ${restoreCollection}: $($_.Exception.Message)"
                }
            }
        }
        break
    }
    "prune" {
        $response = Invoke-QdrantJson -Method Get -Path "/collections/$encodedCollection/snapshots"
        $snapshots = @($response.result | Sort-Object { [DateTime]$_.creation_time } -Descending)
        $deleted = @()
        foreach ($snapshot in ($snapshots | Select-Object -Skip $RetainCount)) {
            $encodedSnapshot = [Uri]::EscapeDataString($snapshot.name)
            Invoke-QdrantJson -Method Delete -Path "/collections/$encodedCollection/snapshots/$encodedSnapshot" | Out-Null
            $deleted += $snapshot.name
        }
        [pscustomobject]@{
            collection = $Collection
            retained = [Math]::Min($snapshots.Count, $RetainCount)
            deleted = $deleted
        }
        break
    }
}
