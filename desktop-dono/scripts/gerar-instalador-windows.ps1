# Gera o instalador do Fila Gestão para Windows (NSIS, .exe), numa máquina Windows de build.
# Uso (PowerShell, na pasta desktop-dono):
#   powershell -ExecutionPolicy Bypass -File scripts\gerar-instalador-windows.ps1 -PainelUrl https://fila.suabarbearia.com.br
# Sem -PainelUrl, usa $env:FILA_PAINEL_URL ou a linha de painel.url.
param([string]$PainelUrl = "")
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

foreach ($cmd in "node", "npm", "cargo") {
  if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
    throw "Falta '$cmd' nesta máquina de build. Veja desktop-dono/README.md (seção 'Máquina de build Windows')."
  }
}
if ($PainelUrl) { $env:FILA_PAINEL_URL = $PainelUrl }
Remove-Item Env:FILA_PAINEL_TESTE_LOCAL -ErrorAction SilentlyContinue

# O Dashboard reaproveita estilos e componentes de frontend/src (e as dependências deles).
npm ci --prefix ..\frontend
if ($LASTEXITCODE -ne 0) { throw "npm ci do frontend falhou" }
npm ci
if ($LASTEXITCODE -ne 0) { throw "npm ci falhou" }
npm run build:windows
if ($LASTEXITCODE -ne 0) { throw "o build falhou (a mensagem 'Fila Gestão: ...' acima diz o motivo)" }

$exe = Get-ChildItem "src-tauri\target\release\bundle\nsis\*-setup.exe" | Sort-Object LastWriteTime | Select-Object -Last 1
Write-Host ""
Write-Host "Instalador gerado:" -ForegroundColor Green
Write-Host "  $($exe.FullName)"
