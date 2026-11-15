param([string]$OutputDirectory = (Join-Path $env:LOCALAPPDATA 'ViewHubCapture'))
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
$goCommand = Get-Command go -ErrorAction SilentlyContinue
$goBinary = if ($goCommand) { $goCommand.Source } else {
    Get-ChildItem (Join-Path $env:USERPROFILE 'go\pkg\mod\golang.org\toolchain@*\bin\go.exe') -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending | Select-Object -First 1 -ExpandProperty FullName
}
if (-not $goBinary) { throw '请先安装 Go，或将 go 添加到 PATH。' }
Push-Location (Join-Path $PSScriptRoot 'web')
try {
    npm ci --ignore-scripts
    if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'Web build failed' }
} finally { Pop-Location }
New-Item -ItemType Directory -Force $OutputDirectory | Out-Null
Push-Location $projectRoot
try {
    & $goBinary test ./apps/capture/...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & $goBinary build -o (Join-Path $OutputDirectory 'viewhub-capture.exe') ./apps/capture/cmd/capture
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed (stop the running client before rebuilding)' }
} finally { Pop-Location }
New-Item -ItemType Directory -Force (Join-Path $OutputDirectory 'web') | Out-Null
Copy-Item (Join-Path $PSScriptRoot 'web\dist\*') (Join-Path $OutputDirectory 'web') -Recurse -Force
Copy-Item (Join-Path $projectRoot 'LICENSE') $OutputDirectory -Force
Copy-Item (Join-Path $projectRoot 'licenses') $OutputDirectory -Recurse -Force
$launcher = '@echo off' + "`r`n" + 'cd /d "%~dp0"' + "`r`n" + 'viewhub-capture.exe -web "%~dp0web"' + "`r`n" + 'pause' + "`r`n"
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'start.cmd'), $launcher)
Write-Output "已构建到 $OutputDirectory，运行 start.cmd，打开 http://127.0.0.1:18491。"
