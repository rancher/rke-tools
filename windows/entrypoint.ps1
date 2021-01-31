<#
    1. recognize the subcommands: kubelet, kube-proxy
    2. output resources to the host
#>

$ErrorActionPreference = 'Stop'
$WarningPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'
$Utf8NoBomEncoding = New-Object System.Text.UTF8Encoding $False

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

# cloud provider
if ($cloudProviderName)
{
    # repair host routes
    Repair-CloudMetadataRoutes -CloudProviderName $cloudProviderName

    # find overriding host name
    $nodeNameOverriding = Get-NodeOverridedName -CloudProviderName $cloudProviderName
    if (-not [string]::IsNullOrEmpty($nodeNameOverriding)) {
        $prcArgs += @(
            "--hostname-override=$nodeNameOverriding"
        )
    }
}

# append passed arguments
$prcArgs += @(Fix-LegacyArgument -ArgumentList $args[1..$args.Length])

$prefixPath = $env:RKE_NODE_PREFIX_PATH
if ([string]::IsNullOrEmpty($prefixPath)) {
	$prefixPath = "c:\"
}

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
                        Copy-Item -Force -Recurse -Destination "c:\host\run\" -Path "$PSScriptRoot\gce-patch\GetGcePdName.dll"
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
                [System.IO.File]::WriteAllLines("c:\host\var\lib\kubelet\config.json", $kubeletDockerConfig, $Utf8NoBomEncoding)
            }
        } catch{
            Log-Warn "Could not put private registry Docker configuration to the host: $($_.Exception.Message)"
        }

        # patch the node address into --node-ip, this would not override that one passing by RKE
        $nodeAddress = Ensure-NodeAddress -InternalAddress $env:RKE_NODE_INTERNAL_ADDRESS -Address $env:RKE_NODE_ADDRESS
        if ($nodeAddress) {
            $prcArgs += @(
                "--node-ip=$nodeAddress"
            )
        }

        Create-Directory -Path "c:\host\etc\kubernetes\bin"
        Transfer-File -Src "c:\etc\kubernetes\bin\kubelet.exe" -Dst "c:\host\etc\kubernetes\bin\kubelet.exe"
        if ($prefixPath -ne "c:\") {
            # copy internally tothe prefix path location so wins can find it
            New-Item "$prefixPath\etc\kubernetes\bin\" -ItemType Directory -ErrorAction 0
            Transfer-File -Src "c:\etc\kubernetes\bin\kubelet.exe" -Dst "$prefixPath\etc\kubernetes\bin\kubelet.exe"
        }

        $prcPath = "$prefixPath\etc\kubernetes\bin\kubelet.exe"
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
            $sourceVip = $subnet.substring(0, $subnet.lastIndexOf(".")) + ".254"
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

        Create-Directory -Path "c:\host\etc\kubernetes\bin"
        Transfer-File -Src "c:\etc\kubernetes\bin\kube-proxy.exe" -Dst "c:\host\etc\kubernetes\bin\kube-proxy.exe"
        if ($prefixPath -ne "c:\") {
            # copy internally to the prefix path location so wins can find it
            New-Item "$prefixPath\etc\kubernetes\bin\" -ItemType Directory -ErrorAction 0
            Transfer-File -Src "c:\etc\kubernetes\bin\kube-proxy.exe" -Dst "$prefixPath\etc\kubernetes\bin\kube-proxy.exe"
        }

        $prcPath = "$prefixPath\etc\kubernetes\bin\kube-proxy.exe"
        $prcExposes = "TCP:10256"
    }

    default {
        Log-Fatal "Could not recognize command '$command'"
    }
}

$prcArgs = Get-UniqueList -Data $prcArgs
Log-Info "Args: $($prcArgs -join ' ')"
wins.exe cli prc run --path "$prcPath" --exposes "$prcExposes" --args "$($prcArgs -join ' ')"
