$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $PSScriptRoot
$frontendRoot = Join-Path $projectRoot "frontend"
$nodeRoot = Join-Path $projectRoot ".tools\node"
$node = Join-Path $nodeRoot "node.exe"
$npmCli = Join-Path $nodeRoot "node_modules\npm\bin\npm-cli.js"

if (-not (Test-Path -LiteralPath $node) -or -not (Test-Path -LiteralPath $npmCli)) {
    throw "Portable Node.js is missing from .tools/node. Install Node.js 24 LTS before starting the frontend."
}

$env:PATH = $nodeRoot + [IO.Path]::PathSeparator + $env:PATH
Push-Location $frontendRoot
try {
    if (-not (Test-Path -LiteralPath "node_modules")) {
        & $node $npmCli ci
    }
    & $node $npmCli run dev
}
finally {
    Pop-Location
}
