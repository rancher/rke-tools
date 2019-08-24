$ErrorActionPreference = 'Stop'
$WarningPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'

function Complete-AzureCloudConfig 
{
	param (
		[parameter(Mandatory = $true)] $CloudConfigPath
	)
	
    try
    {
        # refresh local PATH
        $env:PATH = "c:\opt\rke-tools\azure-cli\python\;c:\opt\rke-tools\azure-cli\python\Scripts\;$($env:PATH)"

        # gain user configruation
        $azCloudConfig = Get-Content -Raw -Path $CloudConfigPath | ConvertTo-JsonObj
        $azureCloud = $azCloudConfig.cloud
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
        $errMsg = az cloud set --name $azureCloud
        if (-not $?) {
            Log-Fatal "Failed to set '$azureCloud' as cloud type: $errMsg"
        }

        # gain resource information
        $azureMetaURL = "http://169.254.169.254/metadata/instance/compute"
        $azLocation = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/location?api-version=2017-08-01&format=text")
        $azResourcesGroup = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/resourceGroupName?api-version=2017-08-01&format=text")
        $azSubscriptionId = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/subscriptionId?api-version=2017-08-01&format=text")
        $azVmName = $(curl.exe  -s -H "Metadata:true" "$azureMetaURL/name?api-version=2017-08-01&format=text")
        if ((-not $azLocation) -or (-not $azSubscriptionId) -or (-not $azResourcesGroup) -or (-not $azVmName)) {
            Log-Warn "Some Azure cloud provider variables were not populated correctly, using the passed cloud provider config"
            return
        }

        # gain network information
        $errMsg = az login --service-principal -u $azureClientId -p $azureClientSecret --tenant $azureTenantId
        if (-not $?) {
            Log-Fatal "Failed to login '$azureCloud' cloud: $errMsg"
        }
        
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
        $azCloudConfig | ConvertTo-Json -Compress -Depth 32 | Out-File -NoNewline -Encoding utf8 -Force -FilePath $CloudConfigPath
        Log-Info "Completed Azure cloud configuration successfully"
    }
    catch 
    {
        Log-Fatal "Failed to complete Azure cloud configuration: $($_.Exception.Message)"
    }
}

Export-ModuleMember -Function Complete-AzureCloudConfig
