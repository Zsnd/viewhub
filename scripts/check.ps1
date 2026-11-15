# Run from any directory. Local tests only: no deployment or camera capture.
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$goCommand = Get-Command go -ErrorAction SilentlyContinue
$goBinary = if ($goCommand) { $goCommand.Source } else {
    Get-ChildItem (Join-Path $env:USERPROFILE 'go/pkg/mod/golang.org/toolchain@*/bin/go.exe') -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending | Select-Object -First 1 -ExpandProperty FullName
}
if (-not $goBinary) { throw '请先安装 Go，或将 go 添加到 PATH。' }
Push-Location $projectRoot
try {
    & $goBinary test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & $goBinary vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
    npm --prefix apps/platform/web test
    if ($LASTEXITCODE -ne 0) { throw 'Platform web tests failed' }
    foreach ($app in @('platform', 'capture')) {
        npm --prefix "apps/$app/web" run build
        if ($LASTEXITCODE -ne 0) { throw "$app web build failed" }
    }
    docker compose config --quiet
    if ($LASTEXITCODE -ne 0) { throw 'Compose config invalid' }
} finally { Pop-Location }
