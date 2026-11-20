# Croc Engine Windows x64 构建脚本
# 用法示例：
#   powershell -ExecutionPolicy Bypass -File .\scripts\build-windows.ps1
#   powershell -ExecutionPolicy Bypass -File .\scripts\build-windows.ps1 -SkipTests
param(
    [string]$GoExe = "",
    [string]$CC = "",
    [string]$DistDir = "",
    [string]$BuildDir = "",
    [int]$RelayPort = 9209,
    [string]$OriginalCroc = "",
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$bridge = Join-Path $root "bridge"
$crocSource = Join-Path $root "croc-go"
if ([string]::IsNullOrWhiteSpace($DistDir))
{
    $DistDir = Join-Path $root "dist\windows-x64"
}
if ([string]::IsNullOrWhiteSpace($BuildDir))
{
    $BuildDir = Join-Path $root ("build\windows-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
}
New-Item -ItemType Directory -Force -Path $DistDir, $BuildDir | Out-Null
$DistDir = (Resolve-Path $DistDir).Path
$BuildDir = (Resolve-Path $BuildDir).Path

function Resolve-Go
{
    param([string]$Explicit)
    if (-not [string]::IsNullOrWhiteSpace($Explicit) -and (Test-Path $Explicit)) { return (Resolve-Path $Explicit).Path }
    if (-not [string]::IsNullOrWhiteSpace($env:CROC_GO_EXE) -and (Test-Path $env:CROC_GO_EXE)) { return (Resolve-Path $env:CROC_GO_EXE).Path }
    $command = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($null -ne $command) { return $command.Source }
    $localGo = Get-ChildItem (Join-Path $env:LOCALAPPDATA "Programs") -Directory -Filter "go1.*" -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending
    foreach ($directory in $localGo)
    {
        $candidate = Join-Path $directory.FullName "bin\go.exe"
        if (Test-Path $candidate) { return $candidate }
    }
    foreach ($candidate in @("C:\Go\bin\go.exe", "C:\Program Files\Go\bin\go.exe"))
    {
        if (Test-Path $candidate) { return $candidate }
    }
    throw "未找到 go.exe；请安装 Go 1.27.3 或传入 -GoExe。"
}

function Resolve-CC
{
    param([string]$Explicit)
    if (-not [string]::IsNullOrWhiteSpace($Explicit) -and (Test-Path $Explicit)) { return (Resolve-Path $Explicit).Path }
    if (-not [string]::IsNullOrWhiteSpace($env:CC) -and (Test-Path $env:CC)) { return (Resolve-Path $env:CC).Path }
    $command = Get-Command gcc.exe -ErrorAction SilentlyContinue
    if ($null -ne $command) { return $command.Source }
    $candidates = @(
        "D:\qt\QT\Tools\mingw1310_64\bin\gcc.exe",
        "C:\msys64\mingw64\bin\gcc.exe",
        "C:\mingw64\bin\gcc.exe"
    )
    foreach ($candidate in $candidates)
    {
        if (Test-Path $candidate) { return $candidate }
    }
    throw "未找到 MinGW-w64 gcc；请安装或传入 -CC。"
}

function Resolve-VisualStudio
{
    $vswhere = Join-Path ${env:ProgramFiles(x86)} "Microsoft Visual Studio\Installer\vswhere.exe"
    if (-not (Test-Path $vswhere))
    {
        throw "未找到 vswhere.exe，无法定位 MSVC 2022。"
    }
    $installation = & $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
    if ([string]::IsNullOrWhiteSpace($installation))
    {
        throw "未找到带 C++ 工具链的 Visual Studio 2022。"
    }
    $msvcRoot = Get-ChildItem (Join-Path $installation "VC\Tools\MSVC") -Directory |
        Sort-Object Name -Descending | Select-Object -First 1
    if ($null -eq $msvcRoot)
    {
        throw "未找到 MSVC 工具目录。"
    }
    return [pscustomobject]@{
        Installation = $installation
        VcVars = Join-Path $installation "VC\Auxiliary\Build\vcvars64.bat"
        Dumpbin = Join-Path $msvcRoot.FullName "bin\Hostx64\x64\dumpbin.exe"
        Lib = Join-Path $msvcRoot.FullName "bin\Hostx64\x64\lib.exe"
    }
}

$go = Resolve-Go $GoExe
$gcc = Resolve-CC $CC
$vs = Resolve-VisualStudio
$goVersion = & $go version
$gccVersion = & $gcc --version | Select-Object -First 1
Write-Host "Go:  $goVersion"
Write-Host "CC:  $gcc"
Write-Host "CC版本: $gccVersion"
Write-Host "VS:  $($vs.Installation)"
if ($goVersion -notmatch "go1\.27\.")
{
    throw "本工程需要 Go 1.27.x（croc 11.6.0 的 go.mod 要求 go 1.27.1）。"
}
if (-not (Test-Path (Join-Path $crocSource "go.mod")))
{
    throw "未找到 Croc 源码目录：$crocSource"
}

if (Test-Path -LiteralPath (Join-Path $crocSource ".git"))
{
    $commit = (& git -C $crocSource rev-parse HEAD).Trim()
    $tag = ""
    try { $tag = (& git -C $crocSource describe --tags --exact-match 2>$null).Trim() } catch { $tag = "" }
    Write-Host "Croc commit: $commit"
    Write-Host "Croc tag:    $tag"
    if ($commit -ne "f707beef66921431eea567887eb94ab7efbfd038")
    {
        throw "Croc 源码不是固定的 v11.6.0 commit f707beef66921431eea567887eb94ab7efbfd038。"
    }
    $dirty = @(& git -C $crocSource status --short)
    Write-Host "Croc 本地库化修改：$($dirty.Count) 项（预期存在）"
}
else
{
    $upstream = Get-Content -LiteralPath (Join-Path $root "UPSTREAM.json") -Raw | ConvertFrom-Json
    $commit = $upstream.commit
    if ($commit -ne "f707beef66921431eea567887eb94ab7efbfd038")
    {
        throw "Unexpected Croc upstream commit in UPSTREAM.json."
    }
    Write-Host "Using vendored Croc source: $($upstream.tag) ($commit)"
}
if (-not $SkipTests -and [string]::IsNullOrWhiteSpace($OriginalCroc))
{
    $crocCommand = Get-Command croc.exe -ErrorAction SilentlyContinue
    if ($null -eq $crocCommand) { throw "Pass -OriginalCroc <croc.exe path> for relay tests, or use -SkipTests." }
    $OriginalCroc = $crocCommand.Source
}

$previousEnvironment = @{}
$environmentNames = @("GOOS", "GOARCH", "CGO_ENABLED", "CC", "CGO_CFLAGS", "GOFLAGS", "GOPROXY", "GOSUMDB")
foreach ($name in $environmentNames)
{
    $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
try
{
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "1"
    $env:CC = $gcc
    $env:CGO_CFLAGS = "-O2"
    $env:GOFLAGS = "-trimpath"

    Push-Location $bridge
    try
    {
        Write-Host "下载 Go 模块依赖..."
        & $go mod download
        if ($LASTEXITCODE -ne 0) { throw "go mod download 失败。" }
        $dll = Join-Path $DistDir "croc_engine.dll"
        Write-Host "构建 croc_engine.dll..."
        & $go build -buildmode=c-shared -o $dll .\cmd\croc_engine
        if ($LASTEXITCODE -ne 0) { throw "Go DLL 构建失败。" }
    }
    finally
    {
        Pop-Location
    }
}
finally
{
    foreach ($name in $environmentNames)
    {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], "Process")
    }
}

$dll = Join-Path $DistDir "croc_engine.dll"
$generatedHeader = Join-Path $DistDir "croc_engine.h"
$publicHeader = Join-Path $bridge "include\croc_engine_api.h"
Copy-Item $publicHeader (Join-Path $DistDir "croc_engine_api.h") -Force
Copy-Item (Join-Path $crocSource "LICENSE") (Join-Path $DistDir "LICENSE") -Force
Copy-Item (Join-Path $crocSource "THIRD_PARTY_NOTICES.md") (Join-Path $DistDir "THIRD_PARTY_NOTICES.md") -Force

$exports = @(
    "Croc_GetApiVersion", "Croc_GetEngineVersion", "Croc_Initialize", "Croc_Shutdown",
    "Croc_StartSend", "Croc_StartReceive", "Croc_CancelTask", "Croc_IsTaskFinished",
    "Croc_GetTaskSnapshot", "Croc_PollEvent", "Croc_RespondToPrompt", "Croc_ReleaseTask",
    "Croc_GetLastError", "Croc_GetTaskCount", "Croc_GetActiveTaskCount", "Croc_GetDroppedEventCount"
)
$defPath = Join-Path $DistDir "croc_engine.def"
"EXPORTS" | Set-Content -Path $defPath -Encoding Ascii
$exports | Add-Content -Path $defPath -Encoding Ascii
$libPath = Join-Path $DistDir "croc_engine.lib"
Write-Host "生成 MSVC 导入库..."
& $vs.Lib /def:$defPath /machine:x64 /out:$libPath | Out-Null
if ($LASTEXITCODE -ne 0) { throw "生成导入库失败。" }

$exportText = (& $vs.Dumpbin /exports $dll | Out-String)
foreach ($name in $exports)
{
    if ($exportText -notmatch ("\b" + [regex]::Escape($name) + "\b"))
    {
        throw "DLL 缺少导出符号：$name"
    }
}
$dependents = (& $vs.Dumpbin /dependents $dll | Out-String)
Write-Host "DLL 依赖："
($dependents -split "`n" | Where-Object { $_ -match "\.dll" }) | ForEach-Object { Write-Host ("  " + $_.Trim()) }
if ($dependents -match "libwinpthread|libgcc|libstdc\+\+")
{
    throw "DLL 仍依赖 MinGW 运行库，不能直接交付 MSVC 应用。"
}

$testSource = Join-Path $root "tests\engine-api\CrocEngineApiTests.cpp"
$testExe = Join-Path $BuildDir "CrocEngineApiTests.exe"
Write-Host "编译 C++20 独立测试程序..."
$compileCommand = "call `"$($vs.VcVars)`" >nul && cl /nologo /utf-8 /std:c++20 /EHsc /O2 /I `"$(Join-Path $bridge 'include')`" `"$testSource`" /Fe:`"$testExe`""
cmd /c $compileCommand
if ($LASTEXITCODE -ne 0 -or -not (Test-Path $testExe))
{
    throw "C++ 测试程序编译失败。"
}

$summary = [ordered]@{
    dll = $dll
    header = (Join-Path $DistDir "croc_engine_api.h")
    import_library = $libPath
    test_exe = $testExe
    build_dir = $BuildDir
    croc_commit = $commit
    go = $goVersion
    compiler = $gccVersion
}
$summary | ConvertTo-Json | Set-Content (Join-Path $BuildDir "build-summary.json") -Encoding UTF8
$summary | Format-List

if (-not $SkipTests)
{
    Write-Host "运行 Windows 验收测试..."
    $testScript = Join-Path $PSScriptRoot "test-windows.ps1"
    $windowsPowerShell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
    & $windowsPowerShell -NoProfile -ExecutionPolicy Bypass -File $testScript -DllPath $dll -TestExe $testExe -WorkDir $BuildDir -RelayPort $RelayPort -GoExe $go -CC $gcc -OriginalCroc $OriginalCroc
    if ($LASTEXITCODE -ne 0) { throw "Windows 验收测试失败。" }
}
Write-Host "构建完成。"
