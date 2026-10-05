$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$apiStdoutLog = Join-Path $repoRoot "api-smoke.stdout.log"
$apiStderrLog = Join-Path $repoRoot "api-smoke.stderr.log"
$externalApiUrl = $env:STREAMFORGE_API_URL
$apiProcess = $null
$environmentKeys = @(
    "STREAMFORGE_DATABASE_MODE",
    "STREAMFORGE_STORAGE_MODE",
    "STREAMFORGE_QUEUE_MODE",
    "STREAMFORGE_API_ADDR",
    "STREAMFORGE_JWT_SECRET",
    "STREAMFORGE_API_URL"
)
$originalEnvironment = @{}
foreach ($key in $environmentKeys) {
    $originalEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
}

try {
    $env:STREAMFORGE_DATABASE_MODE = "memory"
    $env:STREAMFORGE_STORAGE_MODE = "memory"
    $env:STREAMFORGE_QUEUE_MODE = "memory"
    $env:STREAMFORGE_JWT_SECRET = if ($env:STREAMFORGE_JWT_SECRET) { $env:STREAMFORGE_JWT_SECRET } else { "development-only-secret-change-me-32chars" }

    if ($externalApiUrl) {
        $k6Url = $externalApiUrl.TrimEnd("/")
    } else {
        $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
        $listener.Start()
        $port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
        $listener.Stop()

        $env:STREAMFORGE_API_ADDR = "127.0.0.1:$port"
        $k6Url = "http://127.0.0.1:$port"
        $apiProcess = Start-Process -FilePath "go" -ArgumentList "run", ".\apps\api" -WorkingDirectory $repoRoot -RedirectStandardOutput $apiStdoutLog -RedirectStandardError $apiStderrLog -PassThru

        $healthy = $false
        $deadline = (Get-Date).AddSeconds(30)
        while ((Get-Date) -lt $deadline) {
            if ($apiProcess.HasExited) {
                throw "API process exited before becoming healthy. See $apiStderrLog"
            }
            try {
                $response = Invoke-WebRequest -Uri "$k6Url/health" -UseBasicParsing -TimeoutSec 2
                if ($response.StatusCode -eq 200) {
                    $healthy = $true
                    break
                }
            } catch {
                Start-Sleep -Seconds 1
            }
        }
        if (-not $healthy) {
            throw "API did not become healthy on $k6Url"
        }
    }

    $env:STREAMFORGE_API_URL = $k6Url
    & k6 run "$repoRoot\tests\load\api-smoke.js"
    exit $LASTEXITCODE
}
finally {
    if ($apiProcess) {
        $children = Get-CimInstance -ClassName Win32_Process -Filter "ParentProcessId = $($apiProcess.Id)" -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -eq "api.exe" }
        foreach ($child in $children) {
            Stop-Process -Id $child.ProcessId -Force -ErrorAction SilentlyContinue
        }
        if (-not $apiProcess.HasExited) {
            Stop-Process -Id $apiProcess.Id -Force -ErrorAction SilentlyContinue
        }
    }
    foreach ($key in $environmentKeys) {
        if ($null -eq $originalEnvironment[$key]) {
            Remove-Item "Env:$key" -ErrorAction SilentlyContinue
        } else {
            Set-Item "Env:$key" $originalEnvironment[$key]
        }
    }
}
