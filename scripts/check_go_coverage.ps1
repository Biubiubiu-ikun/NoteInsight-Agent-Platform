param(
    [string]$CoverageFile = "backend-go/coverage.out",
    [decimal]$MinimumPercent = 24.0
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $CoverageFile)) {
    throw "Coverage profile not found: $CoverageFile"
}

[long]$totalStatements = 0
[long]$coveredStatements = 0

Get-Content -LiteralPath $CoverageFile | Select-Object -Skip 1 | ForEach-Object {
    $parts = $_ -split '\s+'
    if ($parts.Count -ne 3) {
        throw "Invalid Go coverage profile line: $_"
    }

    $statementCount = [long]::Parse($parts[1], [Globalization.CultureInfo]::InvariantCulture)
    $executionCount = [long]::Parse($parts[2], [Globalization.CultureInfo]::InvariantCulture)
    $totalStatements += $statementCount
    if ($executionCount -gt 0) {
        $coveredStatements += $statementCount
    }
}

if ($totalStatements -eq 0) {
    throw "Coverage profile contains no statements: $CoverageFile"
}

$percent = [decimal]$coveredStatements * 100 / [decimal]$totalStatements
$display = $percent.ToString("F2", [Globalization.CultureInfo]::InvariantCulture)
$minimumDisplay = $MinimumPercent.ToString("F2", [Globalization.CultureInfo]::InvariantCulture)
Write-Host "Go statement coverage: $display% (minimum $minimumDisplay%)"

if ($percent -lt $MinimumPercent) {
    throw "Go coverage $display% is below the required $minimumDisplay%"
}
