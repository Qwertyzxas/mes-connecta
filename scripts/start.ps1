$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host 'Нужен Docker Desktop: https://www.docker.com/products/docker-desktop/'
    exit 1
}
docker info *> $null
if ($LASTEXITCODE -ne 0) {
    Write-Host 'Docker установлен, но не запущен. Откройте Docker Desktop и повторите.'
    exit 1
}

function New-Secret([int]$bytes) {
    $buffer = New-Object byte[] $bytes
    $rng = New-Object System.Security.Cryptography.RNGCryptoServiceProvider
    $rng.GetBytes($buffer)
    -join ($buffer | ForEach-Object { $_.ToString('x2') })
}

if (-not (Test-Path .env)) {
    @(
        'DOMAIN=localhost'
        "POSTGRES_PASSWORD=$(New-Secret 24)"
        "JWT_SECRET=$(New-Secret 32)"
        "S3_SECRET_KEY=$(New-Secret 24)"
        "LIVEKIT_API_SECRET=$(New-Secret 32)"
        'LIVEKIT_CONFIG=./deploy/livekit.local.yaml'
        'ACCESS_TTL=15m'
        'REFRESH_TTL=720h'
    ) | Set-Content -Path .env -Encoding ascii
    Write-Host '.env создан'
}

$envVars = @{}
Get-Content .env | Where-Object { $_ -match '^\s*([^#=]+)=(.*)$' } | ForEach-Object {
    $envVars[$Matches[1].Trim()] = $Matches[2].Trim()
}

if (-not (Test-Path deploy/s3.json)) {
    $secret = $envVars['S3_SECRET_KEY']
    "{`"identities`":[{`"name`":`"connecta`",`"credentials`":[{`"accessKey`":`"connecta`",`"secretKey`":`"$secret`"}],`"actions`":[`"Admin`",`"Read`",`"Write`",`"List`",`"Tagging`"]}]}" |
        Set-Content -Path deploy/s3.json -Encoding ascii
}

docker compose up -d --build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host ''
Write-Host 'Connecta запущена: https://localhost'
Write-Host 'Остановить: docker compose down'
