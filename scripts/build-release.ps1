param([string]$OutputPath = 'output/rotakey-release')

$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot
try {
    if (git diff --name-only HEAD) { throw 'Commit source changes before building a deployable release.' }
    $taskCommit = git rev-parse HEAD
    $taskBuildTime = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    npm --prefix web run build
    if ($LASTEXITCODE -ne 0) { throw 'Admin UI build failed.' }

    # Build from committed source in a fresh directory, then embed the UI. The
    # tracked webdist index is only a development placeholder.
    $taskBuildRoot = Join-Path $taskRoot ('output/release-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $taskBuildRoot -Force | Out-Null
    $taskArchive = Join-Path $taskBuildRoot 'source.tar'
    git archive --format=tar --output=$taskArchive HEAD
    if ($LASTEXITCODE -ne 0) { throw 'Source archive failed.' }
    tar -xf $taskArchive -C $taskBuildRoot
    if ($LASTEXITCODE -ne 0) { throw 'Source extraction failed.' }
    $taskUIRoot = Join-Path $taskBuildRoot 'internal/app/webdist'
    Copy-Item -Path (Join-Path $taskRoot 'web/dist/*') -Destination $taskUIRoot -Recurse -Force
    $taskIndex = Get-Content (Join-Path $taskUIRoot 'index.html') -Raw
    if ($taskIndex -notmatch '/admin/assets/.+\.js' -or $taskIndex -match 'UI is built in the Docker image') {
        throw 'Release has no built admin assets.'
    }

    $taskOutput = [IO.Path]::GetFullPath((Join-Path $taskRoot $OutputPath))
    $taskOldEnvironment = @{}
    foreach ($taskName in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
        $taskOldEnvironment[$taskName] = [Environment]::GetEnvironmentVariable($taskName, 'Process')
    }
    Push-Location $taskBuildRoot
    try {
        $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
        go build -trimpath -ldflags "-s -w -X github.com/jisunahamed/rotakey/internal/app.BuildCommit=$taskCommit -X github.com/jisunahamed/rotakey/internal/app.BuildTime=$taskBuildTime" -o $taskOutput ./cmd/gateway
        if ($LASTEXITCODE -ne 0) { throw 'Gateway build failed.' }
    } finally {
        Pop-Location
        foreach ($taskName in $taskOldEnvironment.Keys) {
            [Environment]::SetEnvironmentVariable($taskName, $taskOldEnvironment[$taskName], 'Process')
        }
    }
    Write-Output "Release built from $taskCommit with production admin assets: $taskOutput"
    Get-FileHash -Algorithm SHA256 -LiteralPath $taskOutput
} finally {
    Pop-Location
}
