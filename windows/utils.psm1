$ErrorActionPreference = 'Stop'
$WarningPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'

function Log-Info
{
    Write-Host -NoNewline -ForegroundColor Blue "INFO: "
    Write-Host -ForegroundColor Gray ("{0,-44}" -f ($Args -join " "))
}

function Log-Warn
{
    Write-Host -NoNewline -ForegroundColor DarkYellow "WARN: "
    Write-Host -ForegroundColor Gray ("{0,-44}" -f ($args -join " "))
}

function Log-Error
{
    Write-Host -NoNewline -ForegroundColor DarkRed "ERRO "
    Write-Host -ForegroundColor Gray ("{0,-44}" -f ($args -join " "))
}


function Log-Fatal
{
    Write-Host -NoNewline -ForegroundColor DarkRed "FATA: "
    Write-Host -ForegroundColor Gray ("{0,-44}" -f ($args -join " "))

    exit 1
}

function ConvertTo-JsonObj
{
    param (
        [parameter(Mandatory = $false, ValueFromPipeline = $true)] [string]$JSON
    )

    if (-not $JSON) {
        return $null
    }

    try {
        $ret = $JSON | ConvertFrom-Json -ErrorAction Ignore -WarningAction Ignore
        return $ret
    } catch {
        return $null
    }
}

function Create-Directory
{
    param (
        [parameter(Mandatory = $false, ValueFromPipeline = $true)] [string]$Path
    )

    if (Test-Path -Path $Path) {
        if (-not (Test-Path -Path $Path -PathType Container)) {
            # clean the same path file
            Remove-Item -Recurse -Force -Path $Path -ErrorAction Ignore | Out-Null
        }

        return
    }

    New-Item -Force -ItemType Directory -Path $Path | Out-Null
}

function Exist-File
{
    param (
        [parameter(Mandatory = $false, ValueFromPipeline = $true)] [string]$Path
    )

    if (Test-Path -Path $Path) {
        if (Test-Path -Path $Path -PathType Leaf) {
            return $true
        }

        # clean the same path directory
        Remove-Item -Recurse -Force -Path $Path -ErrorAction Ignore | Out-Null
    }

    return $false
}

Export-ModuleMember -Function Log-Info
Export-ModuleMember -Function Log-Warn
Export-ModuleMember -Function Log-Error
Export-ModuleMember -Function Log-Fatal
Export-ModuleMember -Function ConvertTo-JsonObj
Export-ModuleMember -Function Create-Directory
Export-ModuleMember -Function Exist-File
