<#
    1. copy rke tools to c:\opt\rke-tools\bin, because Windows doesn't support sharing existing paths
    2. must do `entrypoint.ps1` copy at the last, like a sentinel
    3. run network plugin if required
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

# copy rke tools: service-sidekick -> shared volume -> host or container
try {
    if (-not (Test-Path -PathType Leaf -Path "c:\opt\rke-tools\entrypoint.ps1")) {
        # put cloud provider tools
        switch ($env:RKE_CLOUD_PROVIDER_NAME) {
            "gce" {
                # put GetGcePdName.dll to c:\opt\rke-tools\gce-patch
                Log-Info "Patching GetGcePdName.dll ..."
                Create-Directory -Path "c:\opt\rke-tools\gce-patch"
                Copy-Item -Force -Recurse -Destination "c:\opt\rke-tools\gce-patch\" -Path @(
                    "c:\share\gce-patch\GetGcePdName.dll"
                ) | Out-Null
            }
        }

        # put flexvolume plugins to c:\opt\rke-tools\kubelet\volumeplugins
        Log-Info "Copying kubelet volume plugins ..."
        Create-Directory -Path "c:\opt\rke-tools\kubelet"
        Copy-Item -Force -Recurse -Destination "c:\opt\rke-tools\kubelet\volumeplugins" -Path @(
            "c:\share\kubelet-volumeplugins"
        ) | Out-Null

        # put running binaries to c:\opt\rke-tools\bin
        Log-Info "Copying running binaries ..."
        Create-Directory -Path "c:\opt\rke-tools\bin"
        Copy-Item -Force -Recurse -Destination "c:\opt\rke-tools\bin\" -Path @(
            "c:\Windows\wins.exe"
        ) | Out-Null

        # put runing scripts to c:\opt\rke-tools\
        Log-Info "Copying running scripts ..."
        Copy-Item -Force -Recurse  -Destination "c:\opt\rke-tools\" -Path @(
            "c:\share\scripts\*.psm1"
            "c:\share\scripts\entrypoint.ps1" # entrypoint.ps1 should be put in the bottom as a sentinel
        ) | Out-Null
    }
} catch {
    Log-Fatal "Failed to share the rke-tools: $($_.Exception.Message)"
}

# copy cni related binaries
Create-Directory -Path "c:\host\opt\cni\bin"
Get-ChildItem -Path "c:\opt\cni\bin" | ForEach-Object {
    $fileName = $_.Name
    Transfer-File -Src "c:\opt\cni\bin\$fileName" -Dst "c:\host\opt\cni\bin\$fileName"
}

# process cni network configuration
$clusterCIDR = $env:RKE_CLUSTER_CIDR
$clusterDomain = $env:RKE_CLUSTER_DOMAIN
$clusterDnsServer = $env:RKE_CLUSTER_DNS_SERVER
$clusterServiceCIDR = $env:RKE_CLUSTER_SERVICE_CIDR
$nodeName = $env:RKE_NODE_NAME_OVERRIDE
$cloudProviderName = $env:RKE_CLOUD_PROVIDER_NAME
$cniInfo = @{}

# cloud provider
if ($cloudProviderName)
{
    # repair host routes
    Repair-CloudMetadataRoutes -CloudProviderName $cloudProviderName

    # find overriding host name
    $nodeNameOverriding = Get-NodeOverridedName -CloudProviderName $cloudProviderName
    if (-not [string]::IsNullOrEmpty($nodeNameOverriding)) {
        $nodeName = $nodeNameOverriding
    }
}

# ensure the node network address
$nodeAddress = Ensure-NodeAddress -InternalAddress $env:RKE_NODE_INTERNAL_ADDRESS -Address $env:RKE_NODE_ADDRESS

# output cni network configuration for kubelet
#   windows docker doesn't support host network mode,
#   we need to generate a network configuration to startup kubelet.
$networkConfigObj = $env:RKE_NETWORK_CONFIGURATION | ConvertTo-JsonObj
if ($networkConfigObj) 
{
    switch ($networkConfigObj.plugin)
    {
        "flannel" {
            $options = $networkConfigObj.options
            $type = $options.flannel_backend_type
            if (-not $type) {
                $type = "vxlan"
            }

            $cniConfDelegate = $null
            switch ($type) 
            {
                "vxlan" {
                    $cniConfDelegate = @{
                        type = "win-overlay"
                        dns = @{
                            nameservers = @($clusterDnsServer)
                            search = @(
                                "svc." + $clusterDomain
                            )
                        }
                        policies = @(
                            @{
                                name = "EndpointPolicy"
                                value = @{
                                    Type = "OutBoundNAT"
                                    ExceptionList = @(
                                        $clusterCIDR
                                        $clusterServiceCIDR
                                    )
                                }
                            }
                            @{
                                name = "EndpointPolicy"
                                value = @{
                                    Type = "ROUTE"
                                    NeedEncap = $true
                                    DestinationPrefix = $clusterServiceCIDR
                                }
                            }
                        )
                    }
                    $cniInfo = @{
                        Mode = "overlay"
                        Interface = "vxlan0"
                    }
                }
                "host-gw" {
                    if (-not $nodeAddress) {
                        Log-Fatal "Please indicate the address of this host"
                    }

                    $networkMetadataJSON = wins.exe cli net get --address "$nodeAddress"
                    if (-not $?) {
                        Log-Fatal "Could not get $nodeAddress network adapter: $networkMetadataJSON"
                    }
                    $networkMetadataObj = $networkMetadataJSON | ConvertTo-JsonObj
                    if (-not $networkMetadataObj) {
                        Log-Fatal "Could not convert Network Adapter JSON '$networkMetadataJSON' to object"
                    }

                    $cniConfDelegate = @{
                        type = "win-bridge"
                        dns = @{
                            nameservers = @($clusterDnsServer)
                            search = @(
                                "svc." + $clusterDomain
                            )
                        }
                        policies = @(
                            @{
                                name = "EndpointPolicy"
                                value = @{
                                    Type = "OutBoundNAT"
                                    ExceptionList = @(
                                        $clusterCIDR
                                        $clusterServiceCIDR
                                        $networkMetadataObj.SubnetCIDR
                                    )
                                }
                            }
                            @{
                                name = "EndpointPolicy"
                                value = @{
                                    Type = "ROUTE"
                                    NeedEncap = $true
                                    DestinationPrefix = $clusterServiceCIDR
                                }
                            }
                            @{
                                name = "EndpointPolicy"
                                value = @{
                                    Type = "ROUTE"
                                    NeedEncap = $true
                                    DestinationPrefix = $networkMetadataObj.AddressCIDR
                                }
                            }
                        )
                    }
                    $cniInfo = @{
                        Mode = "l2bridge"
                        Interface = "cbr0"
                    }
                }
            }

            $flannelConflist = @{
                name = $cniInfo.Interface
                cniVersion = "0.2.0"
                plugins = @(
                    @{
                        type = "flannel"
                        capabilities = @{
                            dns = $true
                        }
                        delegate = $cniConfDelegate
                    }
                )
            } | ConvertTo-Json -Compress -Depth 32
            [System.IO.File]::WriteAllText("c:\host\etc\cni\net.d\10-flannel.conflist", $flannelConflist, $Utf8NoBomEncoding)

        }

    }

}

# output cni informantion for kube-proxy
$cniInfoJson = $cniInfo | ConvertTo-Json -Compress -Depth 32
[System.IO.File]::WriteAllText("c:\run\cni-info.json", $cniInfoJson, $Utf8NoBomEncoding)

# start cni management
if ($networkConfigObj) 
{
    switch ($networkConfigObj.plugin) 
    {
        "flannel" {
            $prefixPath = $env:RKE_NODE_PREFIX_PATH
            if ([string]::IsNullOrEmpty($prefixPath)) {
                $prefixPath = "c:\"
            }

            $type = "vxlan"
             # VXLAN Identifier (VNI) to be used, it must be `greater than or equal to 4096` if the cluster includes Windows node
            $vni = 4096
            # UDP port to use for sending encapsulated packets, it must be `equal to 4789` if the cluster includes Windows node
            $port = 4789
            try {
                $options = $networkConfigObj.options

                $ptype = $options.flannel_backend_type
                if ($ptype) {
                    $type = $ptype
                }

                # VXLAN Identifier (VNI) to be used, it must be `greater than or equal to 4096` if the cluster includes Windows node
                $pvni = $options.flannel_backend_vni
                if ($pvni) {
                   $vni = [int]$pvni
                }
            } catch {
                Log-Warn "Could not patch flannel network configration: $($_.Exception.Message)"
            }
            
            $netConf = @{
                Network = $clusterCIDR
                Backend = @{
                    Name = $cniInfo.Interface
                    Type = $type
                    VNI  = $vni
                    Port = $port
                }
            } | ConvertTo-Json -Compress -Depth 32

            [System.IO.File]::WriteAllText("c:\host\etc\kube-flannel\net-conf.json", $netConf, $Utf8NoBomEncoding)

            $flannelArgs = @(
                # could not use kubernetes in-cluster client, indicate kubeconfig instead
                "--kubeconfig-file=$prefixPath\etc\kubernetes\ssl\kubecfg-kube-node.yaml"
                "--ip-masq"
                "--kube-subnet-mgr"
                "--iptables-forward-rules=false"
                "--net-config-path=$prefixPath\etc\kube-flannel\net-conf.json"
            )
            if ($nodeAddress) {
                $flannelArgs += @(
                   "--iface=$nodeAddress"
                )
            }

            Create-Directory -Path "c:\host\opt\bin"
            Transfer-File -Src "c:\opt\bin\flanneld.exe" -Dst "c:\host\opt\bin\flanneld.exe"
            if ($prefixPath -ne "c:\") {
                New-Item "$prefixPath\opt\bin\" -ItemType Directory -ErrorAction 0
                Transfer-File -Src "c:\opt\bin\flanneld.exe" -Dst "$prefixPath\opt\bin\flanneld.exe"
            }

            $winsArgs = $($flannelArgs -join ' ')
            Log-Info "Start flanneld with: $winsArgs"
            wins.exe cli prc run --path "$prefixPath\opt\bin\flanneld.exe" --exposes ("UDP:{0}" -f $port) --envs "NODE_NAME=$nodeName" --args "$winsArgs"
        }
    }

    exit 0
}

Log-Warn "Could not find network configuration from RKE"
ping -t 127.0.0.1 | Out-Null
