param([string]$OutputDirectory = (Join-Path $PSScriptRoot '../../dist/platform'))
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$goCommand = Get-Command go -ErrorAction SilentlyContinue
$goBinary = if ($goCommand) { $goCommand.Source } else {
    Get-ChildItem (Join-Path $env:USERPROFILE 'go/pkg/mod/golang.org/toolchain@*/bin/go.exe') -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending | Select-Object -First 1 -ExpandProperty FullName
}
if (-not $goBinary) { throw '请先安装 Go，或将 go 添加到 PATH。' }
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
Push-Location (Join-Path $PSScriptRoot 'web')
try {
    npm ci --ignore-scripts
    if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'Web build failed' }
} finally { Pop-Location }
New-Item -ItemType Directory -Force $OutputDirectory | Out-Null
$binaryName = if ([Environment]::OSVersion.Platform -eq 'Win32NT') { 'viewhub.exe' } else { 'viewhub' }
Push-Location $projectRoot
try {
    & $goBinary test ./apps/platform/...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & $goBinary build -trimpath -o (Join-Path $OutputDirectory $binaryName) ./apps/platform/cmd/gateway
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
} finally { Pop-Location }
New-Item -ItemType Directory -Force (Join-Path $OutputDirectory 'web') | Out-Null
Copy-Item (Join-Path $PSScriptRoot 'web/dist/*') (Join-Path $OutputDirectory 'web') -Recurse -Force
Write-Output "已构建到 $OutputDirectory；配置 WEB_ROOT 为该目录下的 web，按部署文档配置平台环境。"
