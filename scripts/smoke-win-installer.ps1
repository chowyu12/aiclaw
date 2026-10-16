# Only run on an isolated Windows CI runner: this registers/unregisters AIClaw.
param([Parameter(Mandatory = $true)][string]$Installer)
$ErrorActionPreference = 'Stop'
$Installer = (Resolve-Path $Installer).Path
$installDir = Join-Path $env:TEMP "AIClaw installer smoke $([guid]::NewGuid())"
$markerName = "installer-smoke-$([guid]::NewGuid()).txt"
$dataDirs = @((Join-Path $env:APPDATA 'aiclaw'), (Join-Path $env:USERPROFILE '.aiclaw'))

function Install-App {
    # /D must be last; NSIS accepts the remaining path including its spaces.
    $process = Start-Process -FilePath $Installer -ArgumentList "/S /currentuser /D=$installDir" -PassThru
    if (-not $process.WaitForExit(120000)) { $process.Kill(); throw 'Installer timed out' }
    if ($process.ExitCode -ne 0) { throw "Installer exited with $($process.ExitCode)" }
}

try {
    foreach ($dir in $dataDirs) {
        New-Item -ItemType Directory -Force $dir | Out-Null
        Set-Content (Join-Path $dir $markerName) 'preserve user data'
    }
    Install-App
    foreach ($file in @('AIClaw.exe', 'resources/claw-agent.exe', 'resources/app/package.json')) {
        if (-not (Test-Path (Join-Path $installDir $file))) { throw "Missing installed file: $file" }
    }
    $main = Join-Path $installDir 'resources/app/dist/main/index.js'
    $hash = (Get-FileHash $main).Hash
    Set-Content $main '// old application file fixture'
    Install-App
    if ((Get-FileHash $main).Hash -ne $hash) { throw 'Reinstallation did not replace the old application file' }
    foreach ($dir in $dataDirs) {
        if ((Get-Content (Join-Path $dir $markerName)) -ne 'preserve user data') { throw 'Installation changed user data' }
    }
    $uninstaller = @(Get-ChildItem $installDir -Filter '*uninstall*.exe')
    if ($uninstaller.Count -ne 1) { throw 'Missing uninstaller' }
    $process = Start-Process $uninstaller[0].FullName -ArgumentList "/S /currentuser _?=$installDir" -PassThru
    if (-not $process.WaitForExit(120000)) { $process.Kill(); throw 'Uninstaller timed out' }
    if ($process.ExitCode -ne 0) { throw "Uninstaller exited with $($process.ExitCode)" }
    if (Test-Path (Join-Path $installDir 'AIClaw.exe')) { throw 'Uninstaller left the application executable' }
    foreach ($dir in $dataDirs) {
        if ((Get-Content (Join-Path $dir $markerName)) -ne 'preserve user data') { throw 'Uninstallation changed user data' }
    }
    Write-Output 'Windows installation, replacement and user-data retention checks passed.'
} finally {
    foreach ($dir in $dataDirs) { Remove-Item (Join-Path $dir $markerName) -Force -ErrorAction SilentlyContinue }
    Remove-Item $installDir -Recurse -Force -ErrorAction SilentlyContinue
}
