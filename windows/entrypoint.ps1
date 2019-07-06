<#
    1. recognize the subcommands: kubelet, kube-proxy
    2. output resources to the host
#>

$ErrorActionPreference = 'Stop'
$WarningPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'

Import-Module -WarningAction Ignore -Name @(
    "$PSScriptRoot\utils.psm1"
    "$PSScriptRoot\cloud-provider.psm1"
)

function Wait-Ready 
{
    param(
        [parameter(Mandatory = $true)] $Path
    )

    $count = 30
    while ($count -gt 0) 
    {
        Start-Sleep -s 1

        if (Test-Path $Path -ErrorAction Ignore)
        {
            Start-Sleep -s 5
            break
        }

        Start-Sleep -s 1
        $count -= 1
    }

    if ($count -le 0) 
    {
        Log-Fatal "Timeout, could not found $Path"
    }
}

function Get-UniqueList
{
    param(
        [parameter(Mandatory = $true)] [string[]]$Data
    )
    
    $ret = @()
    $set = @{}
    $Data | ForEach-Object {
        $i = $_
        $t = $i -split '=',2
        if (-not $set.Contains($t[0])) {
            $set.Add($t[0], $true)
            $ret += @($i)
        }
    }

    return $ret
}

function Fix-LegacyArgument
{
    param (
        [parameter(Mandatory = $false)] [string[]]$ArgumentList
    )

    $argList = @()
    $legacy = $null
    for ($i = $ArgumentList.Length; $i -ge 0; $i--) {
        $arg = $ArgumentList[$i]
        switch -regex ($arg)
        {
            "^-{2}.*" {
                if ($legacy) {
                    $arg = '{0}:{1}' -f $arg, $legacy
                    $legacy = $null
                }
                $argList += $arg
            }
            default {
                $legacy = $arg
            }
        }
    }

    return $argList
}

# copy rke tools: shared volume -> container
try 
{
    Copy-Item -Force -Recurse -Destination "c:\Windows\" -Path @(
        "$PSScriptRoot\bin\wins.exe"
    ) | Out-Null
} 
catch 
{
    Log-Fatal "Failed to share rke-tools bins to container: $($_.Exception.Message)"
}

$prcPath = ""
$prcExposes = ""
$prcArgs = @()
$cloudProviderName = $env:RKE_CLOUD_PROVIDER_NAME

# cloud provider: repair host routes
if ($cloudProviderName) 
{
    switch -Regex ($cloudProviderName)
    {
        '^\s*aws\s*$' {
            $errMsg = $(wins.exe cli route add --addresses "169.254.169.254 169.254.169.253 169.254.169.249 169.254.169.123 169.254.169.250 169.254.169.251")
            if (-not $?) {
                Log-Warn "Failed to repair AWS host routes: $errMsg"
            }
        }
        '^\s*azure\s*$' {
            $errMsg = $(wins.exe cli route add --addresses "169.254.169.254")
            if (-not $?) {
                Log-Warn "Failed to repair Azure host routes: $errMsg"
            }
        }
        '^\s*gce\s*$' {
            $errMsg = $(wins.exe cli route add --addresses "169.254.169.254")
            if (-not $?) {
                Log-Warn "Failed to repair GCE host routes: $errMsg"
            }
        }
    }
}

# cloud provider: find overriding host name
$nodeNameOverridingIfNeeded = $null
if (Exist-File -Path "c:\run\cloud-provider-override-hostname") 
{
    $nodeNameOverridingIfNeeded = $(Get-Content -Raw -Path "c:\run\cloud-provider-override-hostname")
} 
elseif($cloudProviderName)
{
    # repair contain route for `169.254.169.254` when using cloud provider
    $actualGateway = $(route.exe PRINT 0.0.0.0 | Where-Object {$_ -match '0\.0\.0\.0.*[a-z]'} | Select-Object -First 1 | ForEach-Object {($_ -replace '0\.0\.0\.0|[a-z]|\s+',' ').Trim() -split ' '} | Select-Object -First 1)
    $expectedGateway = $(route.exe PRINT 169.254.169.254 | Where-Object {$_ -match '169\.254\.169\.254'} | Select-Object -First 1 | ForEach-Object {($_ -replace '169\.254\.169\.254|255\.255\.255\.255|[a-z]|\s+',' ').Trim() -split ' '} | Select-Object -First 1)
    if ($actualGateway -ne $expectedGateway) {
        $errMsg = $(route.exe ADD 169.254.169.254 MASK 255.255.255.255 $actualGateway METRIC 1)
        if (-not $?) {
            Log-Error "Could not repair contain route for using cloud provider"
        }
    }

    switch -Regex ($cloudProviderName)
    {
        '^\s*aws\s*$' {
            # gain private DNS name
            $nodeNameOverridingIfNeeded = $(curl.exe -s "http://169.254.169.254/latest/meta-data/hostname")
            if (-not $?) {
                Log-Error "Failed to gain the priave DNS name for AWS instance: $nodeNameOverridingIfNeeded"
            }
        }
        '^\s*gce\s*$' {
            # gain the host name
            $nodeNameOverridingIfNeeded = $(curl.exe -s -H "Metadata-Flavor: Google" "http://169.254.169.254/computeMetadata/v1/instance/hostname?alt=json")
            if (-not $?) {
                Log-Error "Failed to gain the hostname for GCE instance: $nodeNameOverridingIfNeeded"
            }
        }
    }
}
if ($nodeNameOverridingIfNeeded) {
    $nodeNameOverridingIfNeeded = $nodeNameOverridingIfNeeded.Trim()
    # ouput the overriding name info
    $nodeNameOverridingIfNeeded | Out-File -NoNewline -Encoding utf8 -Force -FilePath "c:\run\cloud-provider-override-hostname"
    Log-Info "Got overriding hostname $nodeNameOverridingIfNeeded"

    $prcArgs += @(
        "--hostname-override=$nodeNameOverridingIfNeeded"
    )
}

# append passed arguments
$prcArgs += @(Fix-LegacyArgument -ArgumentList $args[1..$args.Length])

# deal with the command
$command = $args[0]
switch ($command) 
{
    "kubelet" {
        # cloud provider: kubelet complete
        if ($cloudProviderName) {
            switch -Regex ($cloudProviderName)
            {
                '^\s*azure\s*$' {
                    # complete azure cloud config
                    Complete-AzureCloudConfig -CloudConfigPath "c:\host\etc\kubernetes\cloud-config"
                }
                '^\s*gce\s*$' {
                    # repair Get-GcePdName method
                    # this's a stopgap, we could drop this after https://github.com/kubernetes/kubernetes/issues/74674 fixed
                    try {
                        Copy-Item -Force -Recurse -Destination "c:\run\" -Path "$PSScriptRoot\gce-patch\GetGcePdName.dll"
                    } catch {
                        Log-Warn "Failed to copy GetGcePdName.dll to host: $($_.Exception.Message)"
                    }
                }
            }
        }

        # output the flexvolume plugins
        Copy-Item -ErrorAction Ignore -Force -Recurse -Destination "c:\host\var\lib\kubelet\" -Path "$PSScriptRoot\kubelet\volumeplugins" | Out-Null

        # output the private registry configuration
        try {
            $kubeletDockerConfigB64 = $env:RKE_KUBELET_DOCKER_CONFIG
            if ($kubeletDockerConfigB64) {
                $kubeletDockerConfig = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($kubeletDockerConfigB64))
                $kubeletDockerConfig | Out-File -NoNewline -Encoding utf8 -Force -FilePath "c:\host\var\lib\kubelet\config.json"
            }
        } catch{
            Log-Warn "Could not put private registry Docker configuration to the host: $($_.Exception.Message)"
        }

        $prcPath = "c:\etc\kubernetes\bin\kubelet.exe"
        $prcExposes = "TCP:10250"
    }

    "kube-proxy" {
        # get cni information
        $cniInfoPath = "c:\run\cni-info.json"
        Wait-Ready -Path $cniInfoPath
        $cniConfigJSON = Get-Content -Raw -Path $cniInfoPath
        $cniConfig = $cniConfigJSON | ConvertTo-JsonObj
        if (-not $cniConfig) {
            Log-Fatal "Could not convert CNI Configuration JSON $cniConfigJSON to object"
        }
        if ($cniConfig.Mode -eq "overlay") {
            # get HNS network
            $getHnsNetworkJSON = wins.exe cli hns get-network --name "$($cniConfig.Interface)"
            if (-not $?) {
                Log-Fatal "Could not get $($cniConfig.Interface) HNS network: $getHnsNetworkJSON"
            }
            $hnsNetworkObj = $getHnsNetworkJSON | ConvertTo-JsonObj
            if (-not $hnsNetworkObj) {
                Log-Fatal "Could not convert HNS Network JSON'$getHnsNetworkJSON' to object"
            }

            # create source virtual IP
            $subnet = $hnsNetworkObj.Subnets[0].AddressCIDR
            $sourceVip = $subnet.substring(0, $subnet.lastIndexOf(".")) + ".2"
            $prcArgs += @(
                "--source-vip=$sourceVip"
            )
        }

        # indicate the network interface name
        if ($cniConfig.Interface) {
            $prcArgs += @(
                "--network-name=$($cniConfig.Interface)"
            )
        }

        $prcPath = "c:\etc\kubernetes\bin\kube-proxy.exe"
        $prcExposes = "TCP:10256"
    }

    default {
        Log-Fatal "Could not recognize command '$command'"
    }
}

$prcArgs = Get-UniqueList -Data $prcArgs
Log-Info "Args: $($prcArgs -join ' ')"
wins.exe cli prc run --path "$prcPath" --exposes "$prcExposes" --args "$($prcArgs -join ' ')"
