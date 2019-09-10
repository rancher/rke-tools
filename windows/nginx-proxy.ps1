<#
    1. generate nginx.conf
    2. start nginx
#>

$ErrorActionPreference = 'Stop'
$WarningPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'

Import-Module -WarningAction Ignore -Name "$PSScriptRoot\utils.psm1"

confd.exe -onetime -backend env -log-level error
if (-not $?) {
    Log-Fatal "Failed to generate nginx configuration"
}

Create-Directory -Path "c:\host\etc\nginx"
Transfer-File -Src "c:\etc\nginx\nginx.exe" -Dst "c:\host\etc\nginx\nginx.exe"

wins.exe cli prc run --path "c:\etc\nginx\nginx.exe"
