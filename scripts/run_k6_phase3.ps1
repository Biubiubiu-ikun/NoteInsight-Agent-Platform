param(
    [string]$BaseUrl = "http://host.docker.internal:18080",
    [int]$Vus = 20,
    [string]$Duration = "1m",
    [string]$Image = "grafana/k6:latest"
)

$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $PSScriptRoot
$mount = "${projectRoot}:/work"

docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) {
    docker pull $Image
}

docker run --rm `
    -v $mount `
    -w /work `
    -e BASE_URL=$BaseUrl `
    -e VUS=$Vus `
    -e DURATION=$Duration `
    $Image run load-tests/k6/phase3_notes.js
