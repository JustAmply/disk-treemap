param(
    [switch]$SkipVet
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
$previousLocation = Get-Location
$environmentNames = @("APPDATA", "GOCACHE", "GOPATH", "GOFLAGS")
$previousEnvironment = @{}
foreach ($name in $environmentNames) {
    $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

try {
    Set-Location $repoRoot

    foreach ($directory in @(".appdata", ".gocache", ".gopath")) {
        New-Item -ItemType Directory -Force -Path $directory | Out-Null
    }

    $env:APPDATA = Join-Path $repoRoot ".appdata"
    $env:GOCACHE = Join-Path $repoRoot ".gocache"
    $env:GOPATH = Join-Path $repoRoot ".gopath"
    $env:GOFLAGS = "-trimpath"

    $unformatted = @(& gofmt -l ./cmd ./internal)
    if ($LASTEXITCODE -ne 0) {
        throw "gofmt could not inspect the Go source files."
    }
    if ($unformatted.Count -gt 0) {
        $fileList = $unformatted -join [Environment]::NewLine
        throw "The following Go files are not formatted:$([Environment]::NewLine)$fileList$([Environment]::NewLine)Fix them with: gofmt -w ./cmd ./internal"
    }

    if (-not $SkipVet) {
        & go vet ./...
        if ($LASTEXITCODE -ne 0) {
            throw "go vet failed. Fix the diagnostics above, then rerun scripts/check.ps1."
        }
    }

    & go test ./...
    if ($LASTEXITCODE -ne 0) {
        throw "go test failed. Fix the failing package above, then rerun scripts/check.ps1."
    }

    Write-Host "All Go checks passed."
}
finally {
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], "Process")
    }
    Set-Location $previousLocation
}
