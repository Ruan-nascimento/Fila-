# Roda localmente as mesmas verificações do CI (exceto a varredura de segredos com Docker).
# Uso, na raiz do repositório:   powershell -ExecutionPolicy Bypass -File scripts\verificar.ps1
# Testes com banco: defina antes $env:FILA_TEST_DATABASE_URL (veja o README).
$ErrorActionPreference = 'Stop'
$raiz = Split-Path -Parent $PSScriptRoot
$falhas = @()

function Etapa($nome, [scriptblock]$bloco) {
    Write-Host "`n=== $nome" -ForegroundColor Cyan
    & $bloco
    if ($LASTEXITCODE -ne 0) { $script:falhas += $nome; Write-Host "FALHOU: $nome" -ForegroundColor Red }
}

Push-Location $raiz
try {
    Etapa 'Contrato: rastreabilidade' { python docs/tools/verificar_contrato.py }
    Etapa 'Contrato: lint OpenAPI' { Push-Location docs/contrato; npx --yes '@redocly/cli@2.72.0' lint openapi.yaml; Pop-Location }

    Push-Location backend
    Etapa 'Backend: formatação' { $f = gofmt -l .; if ($f) { $f; $global:LASTEXITCODE = 1 } else { $global:LASTEXITCODE = 0 } }
    Etapa 'Backend: módulos' { go mod verify; if ($LASTEXITCODE -eq 0) { go mod tidy -diff } }
    Etapa 'Backend: go vet' { go vet ./... }
    Etapa 'Backend: staticcheck' { go tool staticcheck ./... }
    Etapa 'Backend: govulncheck' { go tool govulncheck ./... }
    Etapa 'Backend: testes' { go test -count=1 ./... }
    Etapa 'Backend: build' { go build -o ../bin/ ./cmd/... }
    Pop-Location

    Push-Location frontend
    Etapa 'Frontend: dependências' { npm ci --no-fund }
    Etapa 'Frontend: npm audit' { npm audit --audit-level=moderate }
    Etapa 'Frontend: contrato em dia' { npm run check:api }
    Etapa 'Frontend: formatação' { npm run format:check }
    Etapa 'Frontend: lint' { npm run lint }
    Etapa 'Frontend: tipos' { npm run typecheck }
    Etapa 'Frontend: testes' { npm test }
    Etapa 'Frontend: build' { npm run build }
    Pop-Location
}
finally { Pop-Location }

if ($falhas.Count -gt 0) {
    Write-Host "`n$($falhas.Count) verificação(ões) falharam:" -ForegroundColor Red
    $falhas | ForEach-Object { Write-Host " - $_" }
    exit 1
}
Write-Host "`nTodas as verificações passaram." -ForegroundColor Green
if (-not $env:FILA_TEST_DATABASE_URL) {
    Write-Host 'Atenção: FILA_TEST_DATABASE_URL não definida — os testes com banco foram PULADOS.' -ForegroundColor Yellow
}
