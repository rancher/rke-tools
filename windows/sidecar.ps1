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

Import-Module -WarningAction Ignore -Name "$PSScriptRoot\utils.psm1"

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
            "c:\share\scripts\utils.psm1"
            "c:\share\scripts\cloud-provider.psm1"
            "c:\share\scripts\entrypoint.ps1" # entrypoint.ps1 should be put in the bottom as a sentinel
        ) | Out-Null
    }
} catch {
    Log-Fatal "Failed to share the rke-tools: $($_.Exception.Message)"
}

# copy cni related binaries
Create-Directory -Path "c:\host\opt\cni\bin"
Copy-Item -ErrorAction Ignore -Force -Destination "c:\host\opt\cni\bin\" -Path "c:\opt\cni\bin\*.exe"

# process cni network configuration
$clusterCIDR = $env:RKE_CLUSTER_CIDR
$clusterDomain = $env:RKE_CLUSTER_DOMAIN
$clusterDnsServer = $env:RKE_CLUSTER_DNS_SERVER
$clusterServiceCIDR = $env:RKE_CLUSTER_SERVICE_CIDR
$nodeAddress = $env:RKE_NODE_ADDRESS
$nodeInternalAddress = $env:RKE_NODE_INTERNAL_ADDRESS
$cniInfo = @{}

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
                    $networkAddress = ""
                    if ($nodeAddress)  {
                        $networkAddress = $nodeAddress
                    } elseif ($nodeInternalAddress) {
                        $networkAddress = $nodeInternalAddress
                    }

                    $networkMetadataJSON = wins.exe cli net get --address "$networkAddress"
                    if (-not $?) {
                        Log-Fatal "Could not get $networkAddress network adapter: $networkMetadataJSON"
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

            @{
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
            } | ConvertTo-Json -Compress -Depth 32 | Out-File -NoNewline -Encoding utf8 -Force -FilePath "c:\host\etc\cni\net.d\10-flannel.conflist"

        }

    }

}

# output cni informantion for kube-proxy
$cniInfo | ConvertTo-Json -Compress -Depth 32 | Out-File -NoNewline -Encoding utf8 -Force -FilePath "c:\run\cni-info.json"

# start cni management
if ($networkConfigObj) 
{
    switch ($networkConfigObj.plugin) 
    {
        "flannel" {
            $type = $null
            $vni = $null
            $port = $null
            try {
                $options = $networkConfigObj.options

                $type = $options.flannel_backend_type
                if (-not $type) {
                    $type = "vxlan"
                }

                # VXLAN Identifier (VNI) to be used, it must be `greater than or equal to 4096` if the cluster includes Windows node
                $vni = $options.flannel_backend_vni
                if (-not $vni) {
                    $vni = 4096
                } else {
                    $vni = [int]$vni
                }

                # UDP port to use for sending encapsulated packets, it must be `equal to 4789` if the cluster includes Windows node
                $port = 4789
            } catch { }
            
            @{
                Network = $clusterCIDR
                Backend = @{
                    Name = $cniInfo.Interface
                    Type = $type
                    VNI  = $vni
                    Port = $port
                }
            } | ConvertTo-Json -Compress -Depth 32 | Out-File -NoNewline -Encoding utf8 -Force -FilePath "c:\host\etc\kube-flannel\net-conf.json"

            $flannelArgs = @(
                # could not use kubernetes in-cluster client, indicate kubeconfig instead
                "--kubeconfig-file=c:\etc\kubernetes\ssl\kubecfg-kube-node.yaml"
                "--ip-masq"
                "--kube-subnet-mgr"
                "--iptables-forward-rules=false"
            )

            $networkAddress = $null
            if ($nodeInternalAddress)  {
                $networkAddress = $nodeInternalAddress
            } elseif ($nodeAddress) {
                $networkAddress = $nodeAddress
            }
            if ($networkAddress) {
                $flannelArgs += @(
                   "--iface=$networkAddress"
                )
            }

            $nodeName = $env:RKE_NODE_NAME_OVERRIDE
            if (Exist-File -Path "c:\run\cloud-provider-override-hostname") {
                $nodeName = $(Get-Content -Raw -Path "c:\run\cloud-provider-override-hostname")
            }

            $winsArgs = $($flannelArgs -join ' ')
            Log-Info "Start flanneld with: $winsArgs"
            wins.exe cli prc run --path "c:\opt\bin\flanneld.exe" --exposes ("UDP:{0}" -f $port) --args "$winsArgs" --envs "NODE_NAME=$nodeName"
        }
    }

    exit 0
}

Log-Warn "Could not found network configuration from RKE"
ping -t 127.0.0.1 | Out-Null
