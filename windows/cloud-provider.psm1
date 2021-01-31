$ErrorActionPreference = 'Stop'
$WarningPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'
$Utf8NoBomEncoding = New-Object System.Text.UTF8Encoding $False

function Complete-AzureCloudConfig 
{
    param (
        [parameter(Mandatory = $true)] $CloudConfigPath
    )
	
    try
    {
        # refresh local PATH
        # NOTE: The azure-cli executable is accessed via the hyperkube container. The installation happens here:
        # https://github.com/rancher/hyperkube/blob/v1.18.6-rancher1/Dockerfile.windows#L53
        # and the azure-cli build happens here:
        # https://github.com/rancher/azure-cli
        $env:PATH = "c:\azure-cli\python\;c:\azure-cli\python\Scripts\;$($env:PATH)"

        # metadata server config
        $azureMetaURL = "http://169.254.169.254/metadata/instance/compute"
        $azureMetaVersion = "2019-08-15"

        # gain user configruation
        $azCloudConfig = Get-Content -Raw -Path $CloudConfigPath | ConvertTo-JsonObj
        $azureCloud = $azCloudConfig.cloud
        $azureVmType = $azCloudConfig.vmType
        $azureClientId = $azCloudConfig.aadClientId
        $azureClientSecret = $azCloudConfig.aadClientSecret
        $azureTenantId = $azCloudConfig.tenantId

        # verification
        if (-not $azureClientId) {
            Log-Fatal "Could not find 'aadClientId'"
        } 
        if (-not $azureClientSecret) {
            Log-Fatal "Could not find 'aadClientSecret'"
        } 
        if (-not $azureTenantId) {
            Log-Fatal "Could not find 'tenantId'"
        }
        if (-not $azureCloud) {
            $azureCloud = "AzureCloud"
            Log-Warn "Could not find 'cloud', set '$azureCloud' as default"
        }
        if ($azureCloud -eq "AzureUSGovernmentCloud") {
            $azureCloud = "AzureUSGovernment"
        }
        $errMsg = az cloud set --name $azureCloud
        if (-not $?) {
            Log-Fatal "Failed to set '$azureCloud' as cloud type: $errMsg"
        }

        # gain resource information
        $azLocation = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/location?api-version=$azureMetaVersion&format=text")
        $azResourcesGroup = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/resourceGroupName?api-version=$azureMetaVersion&format=text")
        $azSubscriptionId = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/subscriptionId?api-version=$azureMetaVersion&format=text")
        $azVmName = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/name?api-version=$azureMetaVersion&format=text")
        if ((-not $azLocation) -or (-not $azSubscriptionId) -or (-not $azResourcesGroup) -or (-not $azVmName)) {
            Log-Warn "Some Azure cloud provider variables were not populated correctly, using the passed cloud provider config"
            return
        }

        # gain network information
        # NOTE: the escaping syntax around the secret is to ensure the azure-cli doesn't interpret the contents of the secret as a command.
        # See this issue for more information:
        # https://github.com/Azure/azure-cli/issues/8070
        $errMsg = az login --service-principal -u $azureClientId -p "`"$azureClientSecret`"" --tenant $azureTenantId
        if (-not $?) {
            Log-Fatal "Failed to login '$azureCloud' cloud: $errMsg"
        }

        if ($azureVmType -eq "vmss") {
            # instance id in the scale set, usually name_<id> but get from api to be sure
            $azVmScaleSetName = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/vmScaleSetName?api-version=$azureMetaVersion&format=text")
            $azVmInstanceId = (az vmss list-instances -g $azResourcesGroup --name $azVmScaleSetName --query "[?name=='$azVmName'].instanceId" --output tsv).trim()

            $errMsg = (az vmss nic list -g $azResourcesGroup --vmss-name $azVmScaleSetName --query "[0].name" --output 'tsv').trim()
            if (-not $?) {
                Log-Fatal "Failed to get vmss nic: $errMsg"
            }
            $azVmNic = $errMsg

            $errMsg = (((az vmss nic show -g $azResourcesGroup --vmss-name $azVmScaleSetName --name $azVmNic --instance-id $azVmInstanceId --output 'json' --query "ipConfigurations[0].subnet.id") -replace '"', '') -split '/')
            if (-not $?) {
                Log-Fatal "Failed to get subnet of vmss nic '$azVmNic': $errMsg"
            }
            $azVmNicSubnet = $errMsg

            $errMsg = (((az vmss nic show -g $azResourcesGroup --vmss-name $azVmScaleSetName --name $azVmNic --instance-id $azVmInstanceId --output 'json' --query "networkSecurityGroup.id") -replace '"', '') -split '/')
            if (-not $?) {
                Log-Fatal "Failed to get security group of vmss nic '$azVmNic': $errMsg"
            }
            $azVmNicSecurityGroup = $errMsg
        } else {
            $errMsg = (((az vm nic list -g $azResourcesGroup --vm-name $azVmName --output 'json' --query "[0].id") -replace '"', '') -split '/')[8]
            if (-not $?) {
                Log-Fatal "Failed to get vm nic: $errMsg"
            }
            $azVmNic = $errMsg

            $errMsg = (((az vm nic show -g $azResourcesGroup --vm-name $azVmName --nic $azVmNic --output 'json' --query "ipConfigurations[0].subnet.id") -replace '"', '') -split '/')
            if (-not $?) {
                Log-Fatal "Failed to get subnet of vm nic '$azVmNic': $errMsg"
            }
            $azVmNicSubnet = $errMsg

            $errMsg = (((az vm nic show -g $azResourcesGroup --vm-name $azVmName --nic $azVmNic --output 'json' --query "networkSecurityGroup.id") -replace '"', '') -split '/')
            if (-not $?) {
                Log-Fatal "Failed to get security group of vm nic '$azVmNic': $errMsg"
            }
            $azVmNicSecurityGroup = $errMsg
        }

        $null = az logout

        # verification
        $azVnetResourceGroup = $azVmNicSubnet[4]
        $azVnetName = $azVmNicSubnet[8]
        $azSubnetName = $azVmNicSubnet[10]
        $azVmNsg = $azVmNicSecurityGroup[8]
        if ((-not $azVnetResourceGroup) -or (-not $azVnetName) -or (-not $azSubnetName) -or (-not $azVmNsg)) {
            Log-Warn "Some Azure cloud provider variables were not populated correctly, using the passed cloud provider config"
            return
        }

        # override
        $azCloudConfigOverrided = @{
            subscriptionId = $azSubscriptionId
            location = $azLocation
            resourceGroup = $azResourcesGroup
            vnetResourceGroup = $azVnetResourceGroup
            subnetName = $azSubnetName
            useInstanceMetadata = $true
            securityGroupName = $azVmNsg
            vnetName = $azVnetName
        }
        $azCloudConfig = $azCloudConfig | Add-Member -Force -NotePropertyMembers $azCloudConfigOverrided -PassThru

        # output
        $azCloudConfigJson = $azCloudConfig | ConvertTo-Json -Compress -Depth 32
        [System.IO.File]::WriteAllLines($CloudConfigPath, $azCloudConfigJson, $Utf8NoBomEncoding)
        Log-Info "Completed Azure cloud configuration successfully"
    }
    catch 
    {
        Log-Fatal "Failed to complete Azure cloud configuration: $($_.Exception.Message)"
    }
}

function Repair-CloudMetadataRoutes
{
    param (
        [parameter(Mandatory = $false)] $CloudProviderName
    )

    if ($CloudProviderName) {
        switch -Regex ($CloudProviderName) {
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
}

function Get-NodeOverridedName
{
    param (
        [parameter(Mandatory = $false)] $CloudProviderName
    )

    $nodeName = $null

    if (Exist-File -Path "c:\run\cloud-provider-override-hostname") {
        $nodeName = $(Get-Content -Raw -Path "c:\run\cloud-provider-override-hostname")
        if ($nodeName) {
            Log-Info "Got overriding hostname $nodeName from file"
            return $nodeName
        }
    }

    if ($CloudProviderName) {
        try {
            # repair container route for `169.254.169.254` when using cloud provider
            $actualGateway = $(route.exe PRINT 0.0.0.0 | Where-Object {$_ -match '0\.0\.0\.0.*[a-z]'} | Select-Object -First 1 | ForEach-Object {($_ -replace '0\.0\.0\.0|[a-z]|\s+',' ').Trim() -split ' '} | Select-Object -First 1)
            $expectedGateway = $(route.exe PRINT 169.254.169.254 | Where-Object {$_ -match '169\.254\.169\.254'} | Select-Object -First 1 | ForEach-Object {($_ -replace '169\.254\.169\.254|255\.255\.255\.255|[a-z]|\s+',' ').Trim() -split ' '} | Select-Object -First 1)
            if ($actualGateway -ne $expectedGateway) {
                route.exe ADD 169.254.169.254 MASK 255.255.255.255 $actualGateway METRIC 1 | Out-Null
            }
        } catch {
            Log-Error "Failed to repair container route: $($_.Exception.Message)"
        }
        
        switch -Regex ($CloudProviderName) {
            '^\s*aws\s*$' {
                # gain private DNS name
                $nodeName = $(curl.exe -s "http://169.254.169.254/latest/meta-data/hostname")
                if (-not $?) {
                    Log-Error "Failed to gain the priave DNS name for AWS instance: $nodeName"
                    $nodeName = $null
                }
            }
            '^\s*gce\s*$' {
                # gain the host name
                $nodeName = $(curl.exe -s -H "Metadata-Flavor: Google" "http://169.254.169.254/computeMetadata/v1/instance/hostname?alt=json")
                if (-not $?) {
                    Log-Error "Failed to gain the hostname for GCE instance: $nodeName"
                    $nodeName = $null
                }
            }
        }

        if ($nodeName) {
            $nodeName = $nodeName.Trim()
            if ($nodeName -match " ") {
                $nodeName = $nodeName.split(" ")[0] # take the first to be safe
            }
            [System.IO.File]::WriteAllLines("c:\run\cloud-provider-override-hostname", $nodeName, $Utf8NoBomEncoding)
            Log-Info "Got overriding hostname $nodeName from metadata"
        }
    }

    return $nodeName
}

Export-ModuleMember -Function Complete-AzureCloudConfig
Export-ModuleMember -Function Repair-CloudMetadataRoutes
Export-ModuleMember -Function Get-NodeOverridedName
