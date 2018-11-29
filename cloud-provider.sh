#!/bin/bash

AZURE_META_URL="http://169.254.169.254/metadata/instance/compute"
AZURE_CLOUD_CONFIG_PATH="/etc/kubernetes/cloud-config"

set_azure_config() {
  local az_resources_group=$(curl  -s -H Metadata:true "${AZURE_META_URL}/resourceGroupName?api-version=2017-08-01&format=text")
  local az_subscription_id=$(curl -s -H Metadata:true "${AZURE_META_URL}/subscriptionId?api-version=2017-08-01&format=text")
  local az_location=$(curl  -s -H Metadata:true "${AZURE_META_URL}/location?api-version=2017-08-01&format=text")
  local az_vm_name=$(curl -s -H Metadata:true "${AZURE_META_URL}/name?api-version=2017-08-01&format=text")
  local azure_cloud=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .cloud)
  local azure_client_id=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .aadClientId)
  local azure_client_secret=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .aadClientSecret)
  local azure_tenant_id=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .tenantId)
  local az_vm_nsg=$(cat "$AZURE_CLOUD_CONFIG_PATH" | jq -r .securityGroupName)

  # setting correct login cloud
  if [ "${azure_cloud}" = "null" ] || [ "${azure_cloud}" = "" ]; then
      azure_cloud="AzureCloud"
  fi
  az cloud set --name ${azure_cloud}

  # login to Azure
  az login --service-principal -u ${azure_client_id} -p ${azure_client_secret} --tenant ${azure_tenant_id} 2>&1 > /dev/null

  local az_vm_nic=$(az vm nic list -g ${az_resources_group} --vm-name ${az_vm_name} | jq -r .[0].id | cut -d "/" -f 9)
  local az_subnet_name=$(az vm nic show -g ${az_resources_group} --vm-name ${az_vm_name} --nic ${az_vm_nic}| jq -r .ipConfigurations[0].subnet.id| cut -d"/" -f 11)
  local az_vnet_name=$(az vm nic show -g ${az_resources_group} --vm-name ${az_vm_name} --nic ${az_vm_nic}| jq -r .ipConfigurations[0].subnet.id| cut -d"/" -f 9)
  local az_vnet_resource_group=$(az vm nic show -g ${az_resources_group} --vm-name ${az_vm_name} --nic ${az_vm_nic}| jq -r .ipConfigurations[0].subnet.id| cut -d"/" -f 5)

  if [ -z "$az_vm_nsg" ] ; then
    az_vm_nsg=$(az vm nic show -g ${az_resources_group} --vm-name ${az_vm_name} --nic ${az_vm_nic} | jq -r .networkSecurityGroup.id | cut -d "/" -f 9)
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
