# Optional Android arm64 build (not part of the Windows acceptance).
param(
    [string]$GoExe = "",
    [string]$NdkRoot = "",
    [int]$ApiLevel = 26
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$dist = Join-Path $root "dist\android-arm64-v8a"
New-Item -ItemType Directory -Force -Path $dist | Out-Null

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
}
if ([string]::IsNullOrWhiteSpace($NdkRoot))
{
    if (-not [string]::IsNullOrWhiteSpace($env:ANDROID_NDK_HOME)) { $NdkRoot = $env:ANDROID_NDK_HOME }
    else { $NdkRoot = "D:\Android\android-ndk-r27c-windows\android-ndk-r27c" }
}
$clang = Join-Path $NdkRoot "toolchains\llvm\prebuilt\windows-x86_64\bin\aarch64-linux-android$ApiLevel-clang.cmd"
if (-not (Test-Path $clang)) { throw "Android clang wrapper not found: $clang" }

$old = @{}
foreach ($pair in @(@("GOOS", "android"), @("GOARCH", "arm64"), @("CGO_ENABLED", "1"), @("CC", $clang)))
{
    $old[$pair[0]] = [Environment]::GetEnvironmentVariable($pair[0], "Process")
    [Environment]::SetEnvironmentVariable($pair[0], $pair[1], "Process")
}
try
{
    Push-Location (Join-Path $root "bridge")
    try
    {
        & $GoExe build -buildmode=c-shared -trimpath -ldflags '-extldflags "-Wl,-z,max-page-size=16384"' -o (Join-Path $dist "libcroc_engine.so") ./cmd/croc_engine
        if ($LASTEXITCODE -ne 0) { throw "Android build failed." }
    }
    finally { Pop-Location }
}
finally
{
    foreach ($key in $old.Keys) { [Environment]::SetEnvironmentVariable($key, $old[$key], "Process") }
}
Copy-Item (Join-Path $root "bridge\include\croc_engine_api.h") (Join-Path $dist "croc_engine_api.h") -Force
Copy-Item (Join-Path $root "croc-go\LICENSE") (Join-Path $dist "LICENSE") -Force
Copy-Item (Join-Path $root "croc-go\THIRD_PARTY_NOTICES.md") (Join-Path $dist "THIRD_PARTY_NOTICES.md") -Force

$readelf = Join-Path $NdkRoot "toolchains\llvm\prebuilt\windows-x86_64\bin\llvm-readelf.exe"
Get-Item (Join-Path $dist "libcroc_engine.so") | Select-Object Name, Length
if (Test-Path $readelf)
{
    & $readelf -h (Join-Path $dist "libcroc_engine.so") | Select-String -Pattern "Class|Machine|Type"
    & $readelf -d (Join-Path $dist "libcroc_engine.so") | Select-String -Pattern "NEEDED|SONAME"
    & $readelf -l (Join-Path $dist "libcroc_engine.so") | Select-String -Pattern "LOAD" | Select-Object -First 4
}
Write-Host "Android arm64 output: $dist (compile check only; not device-verified)."
