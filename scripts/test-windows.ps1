# Croc Engine Windows acceptance tests.
# Requires a built croc_engine.dll, the C++ test executable, and croc.exe for the relay.
param(
    [Parameter(Mandatory = $true)][string]$DllPath,
    [Parameter(Mandatory = $true)][string]$TestExe,
    [string]$WorkDir = "",
    [int]$RelayPort = 9209,
    [string]$GoExe = "",
    [string]$CC = "",
    [string]$OriginalCroc = "",
    [switch]$SkipCliCompat
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$crocSource = Join-Path $root "croc-go"
$dll = (Resolve-Path $DllPath).Path
$testExe = (Resolve-Path $TestExe).Path
if ([string]::IsNullOrWhiteSpace($WorkDir))
{
    $WorkDir = Join-Path $root ("build\acceptance-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
}
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
$WorkDir = (Resolve-Path $WorkDir).Path
$acceptanceDir = Join-Path $WorkDir ("run-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
New-Item -ItemType Directory -Force -Path $acceptanceDir | Out-Null
$results = [ordered]@{}

function Test-PortOpen
{
    param([string]$HostName, [int]$Port)
    $client = New-Object System.Net.Sockets.TcpClient
    try
    {
        $task = $client.ConnectAsync($HostName, $Port)
        if (-not $task.Wait(500)) { return $false }
        return $client.Connected
    }
    catch { return $false }
    finally { $client.Dispose() }
}

function Wait-ProcessOrKill
{
    param([System.Diagnostics.Process]$Process, [int]$TimeoutSeconds, [string]$Label)
    if (-not $Process.WaitForExit($TimeoutSeconds * 1000))
    {
        try { $Process.Kill() } catch {}
        throw "$Label timed out after ${TimeoutSeconds}s."
    }
    return $Process.ExitCode
}

function Read-LogTail
{
    param([string]$Path, [int]$Lines = 30)
    if (Test-Path $Path)
    {
        return (Get-Content $Path -ErrorAction SilentlyContinue | Select-Object -Last $Lines) -join "`n"
    }
    return ""
}

function New-Code
{
    param([string]$Prefix)
    return $Prefix + "-" + ([guid]::NewGuid().ToString("N").Substring(0, 8))
}

function Assert-FileEqual
{
    param([string]$Left, [string]$Right, [string]$Label)
    if (-not (Test-Path $Left) -or -not (Test-Path $Right))
    {
        throw "$Label file missing: $Left / $Right"
    }
    $leftHash = (Get-FileHash -Algorithm SHA256 $Left).Hash
    $rightHash = (Get-FileHash -Algorithm SHA256 $Right).Hash
    if ($leftHash -ne $rightHash)
    {
        throw "$Label SHA-256 mismatch."
    }
    return $leftHash
}

if ([string]::IsNullOrWhiteSpace($GoExe))
{
    $command = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($null -ne $command) { $GoExe = $command.Source }
    else
    {
        $localGo = Get-ChildItem (Join-Path $env:LOCALAPPDATA "Programs") -Directory -Filter "go1.*" -ErrorAction SilentlyContinue |
            Sort-Object Name -Descending
        foreach ($directory in $localGo)
        {
            $candidate = Join-Path $directory.FullName "bin\go.exe"
            if (Test-Path $candidate) { $GoExe = $candidate; break }
        }
    }
    if ([string]::IsNullOrWhiteSpace($GoExe)) { throw "go.exe not found; pass -GoExe." }
}
if ([string]::IsNullOrWhiteSpace($CC))
{
    $CC = "D:\qt\QT\Tools\mingw1310_64\bin\gcc.exe"
}

$relayPort = $RelayPort
for ($offset = 0; $offset -lt 20; $offset++)
{
    if (-not (Test-PortOpen "127.0.0.1" ($RelayPort + $offset)))
    {
        $relayPort = $RelayPort + $offset
        break
    }
}
$relayLog = Join-Path $acceptanceDir "relay.log"
$relayErr = Join-Path $acceptanceDir "relay.err"
$relayExe = $OriginalCroc
if (-not (Test-Path $relayExe)) { throw "relay executable not found: $relayExe" }
$relay = Start-Process -FilePath $relayExe -ArgumentList @(
    "relay", "--host", "127.0.0.1", "--port", "$relayPort", "--transfers", "5",
    "--source-join-limit", "1000", "--room-join-limit", "1000"
) -RedirectStandardOutput $relayLog -RedirectStandardError $relayErr -PassThru -WindowStyle Hidden

try
{
    $ready = $false
    for ($i = 0; $i -lt 50; $i++)
    {
        if (Test-PortOpen "127.0.0.1" $relayPort) { $ready = $true; break }
        Start-Sleep -Milliseconds 100
    }
    if (-not $ready) { throw "relay did not become ready on 127.0.0.1:$relayPort." }
    Write-Host "relay 127.0.0.1:$relayPort (pid $($relay.Id))"

    $suiteLog = Join-Path $acceptanceDir "cpp-suite.log"
    $suiteErr = Join-Path $acceptanceDir "cpp-suite.err"
    $suite = Start-Process -FilePath $testExe -ArgumentList @(
        "--relay", "127.0.0.1:$relayPort", "--dll", $dll,
        "--work", (Join-Path $acceptanceDir "suite"), "--timeout", "120"
    ) -RedirectStandardOutput $suiteLog -RedirectStandardError $suiteErr -PassThru -WindowStyle Hidden
    Wait-ProcessOrKill $suite 240 "C++ API test"
    Get-Content $suiteLog -ErrorAction SilentlyContinue | ForEach-Object { Write-Host $_ }
    if ((Get-Content $suiteLog -Raw) -notmatch "ALL TESTS PASSED") { throw "C++ API test failed.`n$(Read-LogTail $suiteErr)" }
    $results["cpp_api_suite"] = "passed"

    if (-not $SkipCliCompat)
    {
        $cliDir = Join-Path $root "build\cli"
        New-Item -ItemType Directory -Force -Path $cliDir | Out-Null
        $cliExe = Join-Path $cliDir "croc-cli.exe"
        Write-Host "Building Croc v11.6.0 CLI peer..."
        $oldValues = @{}
        foreach ($pair in @(@("GOOS", "windows"), @("GOARCH", "amd64"), @("CGO_ENABLED", "1"), @("CC", $CC)))
        {
            $oldValues[$pair[0]] = [Environment]::GetEnvironmentVariable($pair[0], "Process")
            [Environment]::SetEnvironmentVariable($pair[0], $pair[1], "Process")
        }
        try
        {
            Push-Location $crocSource
            try { & $GoExe build -trimpath -o $cliExe . }
            finally { Pop-Location }
            if ($LASTEXITCODE -ne 0 -or -not (Test-Path $cliExe)) { throw "Croc CLI build failed." }
        }
        finally
        {
            foreach ($key in $oldValues.Keys)
            {
                [Environment]::SetEnvironmentVariable($key, $oldValues[$key], "Process")
            }
        }

        $source = Join-Path $acceptanceDir "protocol.bin"
        $sourceBytes = New-Object byte[] (5 * 1024 * 1024)
        (New-Object System.Random 20261101).NextBytes($sourceBytes)
        [IO.File]::WriteAllBytes($source, $sourceBytes)

        # CLI sender -> DLL receiver
        $code = New-Code "d1l2"
        $recvDir = Join-Path $acceptanceDir "cli-to-dll"
        New-Item -ItemType Directory -Force -Path $recvDir | Out-Null
        $peerOut = Join-Path $acceptanceDir "dll-receiver.out"
        $peerErr = Join-Path $acceptanceDir "dll-receiver.err"
        $peer = Start-Process -FilePath $testExe -ArgumentList @(
            "--mode", "peer-receive", "--code", $code, "--save", $recvDir,
            "--relay", "127.0.0.1:$relayPort", "--dll", $dll, "--timeout", "60"
        ) -RedirectStandardOutput $peerOut -RedirectStandardError $peerErr -PassThru -WindowStyle Hidden
        Start-Sleep -Milliseconds 800
        $cliOut = Join-Path $acceptanceDir "cli-sender.out"
        $cliErr = Join-Path $acceptanceDir "cli-sender.err"
        $sender = Start-Process -FilePath $cliExe -ArgumentList @(
            "--relay", "127.0.0.1:$relayPort", "--pass", "pass123", "--disable-clipboard",
            "send", "--code", $code, "--no-local", $source
        ) -RedirectStandardOutput $cliOut -RedirectStandardError $cliErr -PassThru -WindowStyle Hidden
        Wait-ProcessOrKill $peer 90 "DLL receiver"
        Wait-ProcessOrKill $sender 90 "CLI sender"
        if ((Get-Content $peerOut -Raw) -notmatch "verification=passed")
        {
            throw "CLI sender -> DLL receiver verification failed.`n$(Get-Content $peerOut -Raw)"
        }
        $hash = Assert-FileEqual $source (Join-Path $recvDir "protocol.bin") "CLI sender -> DLL receiver"
        Write-Host "  [ok] CLI sender -> DLL receiver, SHA-256 $hash"
        $results["cli_to_dll"] = "passed"

        # DLL sender -> CLI receiver
        $code = New-Code "d3l4"
        $recvDir = Join-Path $acceptanceDir "dll-to-cli"
        New-Item -ItemType Directory -Force -Path $recvDir | Out-Null
        $cliOut = Join-Path $acceptanceDir "cli-receiver.out"
        $cliErr = Join-Path $acceptanceDir "cli-receiver.err"
        $receiver = Start-Process -FilePath $cliExe -ArgumentList @(
            "--relay", "127.0.0.1:$relayPort", "--pass", "pass123", "--yes", "--disable-clipboard", $code
        ) -WorkingDirectory $recvDir -RedirectStandardOutput $cliOut -RedirectStandardError $cliErr -PassThru -WindowStyle Hidden
        Start-Sleep -Milliseconds 800
        $peerOut = Join-Path $acceptanceDir "dll-sender.out"
        $peerErr = Join-Path $acceptanceDir "dll-sender.err"
        $peer = Start-Process -FilePath $testExe -ArgumentList @(
            "--mode", "peer-send", "--code", $code, "--file", $source,
            "--relay", "127.0.0.1:$relayPort", "--dll", $dll, "--timeout", "60"
        ) -RedirectStandardOutput $peerOut -RedirectStandardError $peerErr -PassThru -WindowStyle Hidden
        Wait-ProcessOrKill $peer 90 "DLL sender"
        Wait-ProcessOrKill $receiver 90 "CLI receiver"
        $hash = Assert-FileEqual $source (Join-Path $recvDir "protocol.bin") "DLL sender -> CLI receiver"
        Write-Host "  [ok] DLL sender -> CLI receiver, SHA-256 $hash"
        $results["dll_to_cli"] = "passed"

        # Additional: the user's prebuilt engine/croc.exe as sender. Its receiver
        # direction is not claimed because that binary fails file creation on this
        # machine; see docs/croc-engine-api.md known limitations.
        if ((Test-Path $OriginalCroc) -and ((Resolve-Path $OriginalCroc).Path -ne (Resolve-Path $cliExe).Path))
        {
            $code = New-Code "o5r6"
            $recvDir = Join-Path $acceptanceDir "original-to-dll"
            New-Item -ItemType Directory -Force -Path $recvDir | Out-Null
            $peerOut = Join-Path $acceptanceDir "original-peer.out"
            $peerErr = Join-Path $acceptanceDir "original-peer.err"
            $peer = Start-Process -FilePath $testExe -ArgumentList @(
                "--mode", "peer-receive", "--code", $code, "--save", $recvDir,
                "--relay", "127.0.0.1:$relayPort", "--dll", $dll, "--timeout", "60"
            ) -RedirectStandardOutput $peerOut -RedirectStandardError $peerErr -PassThru -WindowStyle Hidden
            Start-Sleep -Milliseconds 800
            $origOut = Join-Path $acceptanceDir "original-sender.out"
            $origErr = Join-Path $acceptanceDir "original-sender.err"
            $sender = Start-Process -FilePath $OriginalCroc -ArgumentList @(
                "--relay", "127.0.0.1:$relayPort", "--pass", "pass123", "--disable-clipboard",
                "send", "--code", $code, "--no-local", $source
            ) -RedirectStandardOutput $origOut -RedirectStandardError $origErr -PassThru -WindowStyle Hidden
            try
            {
                Wait-ProcessOrKill $peer 90 "DLL receiver (prebuilt sender)"
                Wait-ProcessOrKill $sender 90 "prebuilt CLI sender"
                $hash = Assert-FileEqual $source (Join-Path $recvDir "protocol.bin") "prebuilt CLI -> DLL"
                Write-Host "  [ok] prebuilt engine/croc.exe sender -> DLL receiver, SHA-256 $hash"
                $results["original_cli_to_dll"] = "passed"
            }
            catch
            {
                $results["original_cli_to_dll"] = "failed"
                Write-Warning "prebuilt engine/croc.exe sender test failed: $($_.Exception.Message)"
            }
        }
    }

    $results["relay"] = "127.0.0.1:$relayPort"
    $results["dll"] = $dll
    $results["test_exe"] = $testExe
    $results["acceptance_dir"] = $acceptanceDir
    $results | ConvertTo-Json | Set-Content (Join-Path $acceptanceDir "results.json") -Encoding UTF8
    Write-Host "Acceptance tests passed. Results: $(Join-Path $acceptanceDir 'results.json')"
    exit 0
}
catch
{
    Write-Error $_.Exception.Message
    $results["cpp_api_suite"] = "failed"
    $results["error"] = $_.Exception.Message
    $results["acceptance_dir"] = $acceptanceDir
    $results | ConvertTo-Json | Set-Content (Join-Path $acceptanceDir "results.json") -Encoding UTF8
    exit 1
}
finally
{
    if ($null -ne $relay -and -not $relay.HasExited)
    {
        try { Stop-Process -Id $relay.Id -Force } catch {}
    }
}
