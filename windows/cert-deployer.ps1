$ErrorActionPreference = 'Stop'
$WarningPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'

Import-Module -WarningAction Ignore -Name "$PSScriptRoot\utils.psm1"
function Normal-Format
{
    param (
        [parameter(Mandatory = $false, ValueFromPipeline = $true)] [string]$Val
    )

    return $Val.ToLower() -replace '_','-'
}

$sslCertsDir = "c:\etc\kubernetes\ssl" # dir on the container
Create-Directory -Path $sslCertsDir

# output pem file
Get-ChildItem Env: | Select-Object -Property Key,Value | Where-Object {$_.Key -cmatch "^KUBE_"} | ForEach-Object {
    $key = $_.Key
    $val = $_.Value

    $path = "$sslCertsDir\$($key | Normal-Format).pem"
    if ((-not (Exist-File -Path $path)) -or ($env:FORCE_DEPLOY -eq "true")) {
        $val | Out-File -NoNewline -Encoding ascii -Force -FilePath $path
    }
}
Log-Info "Outputted PEM files for Kubernetes components"

# output yaml file
Get-ChildItem Env: | Select-Object -Property Key,Value | Where-Object {$_.Key -cmatch "^KUBECFG_"} | ForEach-Object {
    $key = $_.Key
    $val = $_.Value

    $path = "$sslCertsDir\$($key | Normal-Format).yaml"
    if (-not (Exist-File -Path $path)) {
        $val | Out-File -NoNewline -Encoding ascii -Force -FilePath $path
    }
}
Log-Info "Outputted YAML files for Kubernetes components"