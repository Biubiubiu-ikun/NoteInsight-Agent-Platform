param(
    [Parameter(Mandatory = $true)]
    [string]$BackupFile,
    [switch]$ConfirmRestore
)

$ErrorActionPreference = "Stop"
if (-not $ConfirmRestore) {
    throw "Restore replaces database objects. Re-run with -ConfirmRestore after verifying the target stack."
}
$resolved = (Resolve-Path -LiteralPath $BackupFile).Path
$containerFile = "/tmp/noteinsight_restore.dump"

docker cp $resolved "creatorinsight-postgres`:$containerFile"
if ($LASTEXITCODE -ne 0) { throw "docker cp failed" }
docker exec creatorinsight-postgres pg_restore -U creatorinsight -d creatorinsight --clean --if-exists --no-owner $containerFile
if ($LASTEXITCODE -ne 0) { throw "pg_restore failed" }
docker exec creatorinsight-postgres rm -f $containerFile
if ($LASTEXITCODE -ne 0) { throw "temporary dump cleanup failed" }

Write-Host "Restore completed. Run migrations, reconcile, and smoke tests before reopening traffic."
