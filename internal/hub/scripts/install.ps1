# Installs the Lotse agent as a Windows service. Run in an elevated PowerShell:
# & ([scriptblock]::Create((irm HUB/install.ps1))) -Hub HUB -Key 'ssh-ed25519 ...' -Token TOKEN [-AllowShell] [-NoUpdates]
param(
    [Parameter(Mandatory = $true)][string]$Hub,
    [Parameter(Mandatory = $true)][string]$Key,
    [string]$Token = "",
    [switch]$AllowShell,
    [switch]$NoUpdates
)
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue" # the progress bar makes downloads very slow in PowerShell 5

$name = "lotse-agent"
$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Please run PowerShell as Administrator."
}

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$dir = Join-Path $env:ProgramFiles $name
$exe = Join-Path $dir "$name.exe"
New-Item -ItemType Directory -Force -Path $dir | Out-Null

# A running service locks its executable; stop it before replacing the file.
$svc = Get-Service -Name $name -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne "Stopped") { Stop-Service -Name $name -Force }

Write-Host "Downloading $name for windows/$arch from $Hub ..."
$tmp = "$exe.download"
Invoke-WebRequest -UseBasicParsing -Uri "$Hub/download/$name-windows-$arch.exe" -OutFile $tmp
Move-Item -Force $tmp $exe

$shell = if ($AllowShell) { "true" } else { "false" }
$updates = if ($NoUpdates) { "true" } else { "false" }
& $exe install "--hub=$Hub" "--key=$Key" "--token=$Token" "--allow-shell=$shell" "--no-updates=$updates"
if ($LASTEXITCODE -ne 0) { throw "agent installation failed" }
