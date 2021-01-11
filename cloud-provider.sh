#!/bin/bash

AZURE_META_URL="http://169.254.169.254/metadata/instance/compute"
AZURE_META_API_VERSION="2019-08-15"
AZURE_CLOUD_CONFIG_PATH="/etc/kubernetes/cloud-config"

set_azure_config() {
  set +x
  local az_resources_group=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .resourceGroup)
  local az_subscription_id=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .subscriptionId)
  local az_location=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .location)
  local azure_cloud=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .cloud)
  local azure_client_id=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .aadClientId)
  local azure_client_secret=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .aadClientSecret)
  local azure_tenant_id=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .tenantId)
  local az_vm_nsg=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .securityGroupName)
  local az_vnet_resource_group=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .vnetResourceGroup)
  local az_subnet_name=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .subnetName)
  local az_vnet_name=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .vnetName)
  local az_vm_type=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .vmType)
  local az_managed_identity_extension=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .useManagedIdentityExtension)

  local az_vm_resources_group=$(curl  -s -H Metadata:true "${AZURE_META_URL}/resourceGroupName?api-version=${AZURE_META_API_VERSION}&format=text")
  local az_vm_name=$(curl -s -H Metadata:true "${AZURE_META_URL}/name?api-version=${AZURE_META_API_VERSION}&format=text")

  # setting correct login cloud
  if [ "${azure_cloud}" = "null" ] || [ "${azure_cloud}" = "" ]; then
    azure_cloud="AzureCloud"
  fi
  if [ "${azure_cloud}" = "AzureUSGovernmentCloud" ]; then
    azure_cloud="AzureUSGovernment" # naming issue with azure cli
  fi
  az cloud set --name ${azure_cloud}

  # login to Azure
  if [ "$az_managed_identity_extension" = "true" ] && [ "${azure_client_secret}" = "" ]; then
    echo "using MSI for az login"
    az login --identity 2>&1 > /dev/null
  else
    az login --service-principal -u ${azure_client_id} -p ${azure_client_secret} --tenant ${azure_tenant_id} 2>&1 > /dev/null
  fi
  # set subscription to be the current active subscription
  az account set --subscription ${az_subscription_id}

  if [ -z "$az_resources_group" ] ; then
    az_resources_group="$az_vm_resources_group"
  fi

  if [ -z "$az_location" ]; then
    az_location=$(curl  -s -H Metadata:true "${AZURE_META_URL}/location?api-version=${AZURE_META_API_VERSION}&format=text")
  fi

  if [ "$az_vm_type" = "vmss" ]; then
    # vmss
    local az_vm_scale_set_name=$(curl  -s -H Metadata:true "${AZURE_META_URL}/vmScaleSetName?api-version=${AZURE_META_API_VERSION}&format=text")
    local az_vm_instance_id=$(az vmss list-instances -g ${az_resources_group} --name ${az_vm_scale_set_name} --query "[?name=='${az_vm_name}'].instanceId" --output tsv)
    local az_vm_nic=$(az vmss nic list -g ${az_resources_group} --vmss-name ${az_vm_scale_set_name} --output tsv --query [0].name)

    if [ -z "$az_subnet_name" ] ; then
      az_subnet_name=$(az vmss nic show -g ${az_resources_group} --vmss-name ${az_vm_scale_set_name} --name ${az_vm_nic} --instance-id ${az_vm_instance_id} | jq -r .ipConfigurations[0].subnet.id | cut -d "/" -f 11)
    fi

    if [ -z "$az_vnet_name" ] ; then
      az_vnet_name=$(az vmss nic show -g ${az_resources_group} --vmss-name ${az_vm_scale_set_name} --name ${az_vm_nic} --instance-id ${az_vm_instance_id}  | jq -r .ipConfigurations[0].subnet.id | cut -d "/" -f 9)
    fi

    if [ -z "$az_vnet_resource_group" ] ; then
      az_vnet_resource_group=$(az vmss nic show -g ${az_resources_group} --vmss-name ${az_vm_scale_set_name} --name ${az_vm_nic} --instance-id ${az_vm_instance_id} | jq -r .ipConfigurations[0].subnet.id | cut -d "/" -f 5)
    fi

    if [ -z "$az_vm_nsg" ] ; then
      az_vm_nsg=$(az vmss nic show -g ${az_resources_group} --vmss-name ${az_vm_scale_set_name} --name ${az_vm_nic} --instance-id ${az_vm_instance_id} | jq -r .networkSecurityGroup.id | cut -d "/" -f 9)
    fi
  else
    # standard, vm
    local az_vm_nic=$(az vm nic list -g ${az_resources_group} --vm-name ${az_vm_name} | jq -r .[0].id | cut -d "/" -f 9)

    if [ -z "$az_subnet_name" ] ; then
      az_subnet_name=$(az vm nic show -g ${az_resources_group} --vm-name ${az_vm_name} --nic ${az_vm_nic} | jq -r .ipConfigurations[0].subnet.id | cut -d "/" -f 11)
    fi

    if [ -z "$az_vnet_name" ] ; then
      az_vnet_name=$(az vm nic show -g ${az_resources_group} --vm-name ${az_vm_name} --nic ${az_vm_nic} | jq -r .ipConfigurations[0].subnet.id | cut -d "/" -f 9)
    fi

    if [ -z "$az_vnet_resource_group" ] ; then
      az_vnet_resource_group=$(az vm nic show -g ${az_resources_group} --vm-name ${az_vm_name} --nic ${az_vm_nic} | jq -r .ipConfigurations[0].subnet.id | cut -d "/" -f 5)
    fi

    if [ -z "$az_vm_nsg" ] ; then
      az_vm_nsg=$(az vm nic show -g ${az_resources_group} --vm-name ${az_vm_name} --nic ${az_vm_nic} | jq -r .networkSecurityGroup.id | cut -d "/" -f 9)
    fi
  fi

  az logout 2>&1 > /dev/null

  if [ -z "$az_subscription_id" ] || [ -z "$az_location" ] || [ -z "$az_resources_group" ] || [ -z "$az_vnet_resource_group" ] || [ -z "$az_subnet_name" ] || [ -z "$az_vnet_name" ] || [ -z "$az_vm_nsg" ]; then
    echo "Some variables were not populated correctly, using the passed config!"
  else
    local cloud_config_temp=$(mktemp)
    cat "$AZURE_CLOUD_CONFIG_PATH" |\
    jq '.subscriptionId=''"'${az_subscription_id}'"''' |\
    jq '.location=''"'${az_location}'"''' |\
    jq '.resourceGroup=''"'${az_resources_group}'"''' |\
    jq '.vnetResourceGroup=''"'${az_vnet_resource_group}'"''' |\
    jq '.subnetName=''"'${az_subnet_name}'"''' |\
    jq '.useInstanceMetadata=true' |\
    jq '.securityGroupName=''"'${az_vm_nsg}'"''' |\
    jq '.vnetName=''"'${az_vnet_name}'"''' > $cloud_config_temp
    # move the temp to the azure cloud config path
    mv $cloud_config_temp $AZURE_CLOUD_CONFIG_PATH
  fi
}
