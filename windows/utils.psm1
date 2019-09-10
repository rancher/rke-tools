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

function Ensure-NodeAddress
{
    param(
        [parameter(Mandatory = $false)] $Address = "",
        [parameter(Mandatory = $false)] $InternalAddress = ""
    )

    if (-not [string]::IsNullOrEmpty($InternalAddress)) {
        if ($InternalAddress -ne $Address) {
            return $InternalAddress
        }

        # if they are the same address, we need to verify that does the internal address correspond to a real network interface?
        $null = wins.exe cli net get --address "$InternalAddress"
        if ($?) {
            return $InternalAddress
        }

        # else we try to return the default network interface
        $defaultAdapterJson = wins.exe cli net get
        if ($?) {
            $defaultNetwork = $defaultAdapterJson | ConvertTo-JsonObj
            if ($defaultNetwork) {
                return ($defaultNetwork.AddressCIDR -replace "/32","")
            }
        }

        return $InternalAddress
    }

    return $Address
}

function Transfer-File
{
    param (
        [parameter(Mandatory = $true)] [string]$Src,
        [parameter(Mandatory = $true)] [string]$Dst
    )

    if (Test-Path -PathType leaf -Path $Dst) {
        $dstHasher = Get-FileHash -Path $Dst
        $srcHasher = Get-FileHash -Path $Src
        if ($dstHasher.Hash -eq $srcHasher.Hash) {
            return
        }
    }

    try {
        $null = Copy-Item -Force -Path $Src -Destination $Dst
    } catch {
        Log-Fatal "Could not transfer file $Src to $Dst : $($_.Exception.Message)"
    }
}

Export-ModuleMember -Function Log-Info
Export-ModuleMember -Function Log-Warn
Export-ModuleMember -Function Log-Error
Export-ModuleMember -Function Log-Fatal
Export-ModuleMember -Function ConvertTo-JsonObj
Export-ModuleMember -Function Create-Directory
Export-ModuleMember -Function Exist-File
Export-ModuleMember -Function Ensure-NodeAddress
Export-ModuleMember -Function Transfer-File
