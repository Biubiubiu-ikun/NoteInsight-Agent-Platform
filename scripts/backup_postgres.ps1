param(
    [string]$OutputDirectory = "artifacts/backups"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$resolvedOutput = Join-Path $root $OutputDirectory
New-Item -ItemType Directory -Force -Path $resolvedOutput | Out-Null
$timestamp = Get-Date -Format "yyyyMMdd_HHmmss"
$containerFile = "/tmp/noteinsight_$timestamp.dump"
$outputFile = Join-Path $resolvedOutput "noteinsight_$timestamp.dump"

docker exec creatorinsight-postgres pg_dump -U creatorinsight -d creatorinsight -Fc -f $containerFile
if ($LASTEXITCODE -ne 0) { throw "pg_dump failed" }
docker cp "creatorinsight-postgres`:$containerFile" $outputFile
if ($LASTEXITCODE -ne 0) { throw "docker cp failed" }
docker exec creatorinsight-postgres rm -f $containerFile
if ($LASTEXITCODE -ne 0) { throw "temporary dump cleanup failed" }

Write-Host "Backup created: $outputFile"
