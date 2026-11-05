// Bunker on a small Azure Linux VM: the recommended way to run Bunker on Azure.
//
// What you get: an Ubuntu 24.04 VM, a separate managed data disk for Bunker's
// SQLite database and files (survives VM rebuilds, easy to snapshot), a free
// DNS name (<label>.<region>.cloudapp.azure.com) with automatic HTTPS via
// Caddy, and a firewall that only allows 80/443 (SSH optional).
//
//   az group create -n bunker -l westeurope
//   az deployment group create -g bunker -f deploy/azure/main.bicep \
//     -p adminPasswordOrKey="$(cat ~/.ssh/id_ed25519.pub)" authenticationType=sshPublicKey
//
// Or click "Deploy to Azure" in DEPLOY.md, which uses the compiled template:
//   az bicep build -f deploy/azure/main.bicep --outfile deploy/azure/azuredeploy.json
//
// Outputs: the URL, the setup page and the one-time setup code. Bunker is
// usually ready 3-5 minutes after the deployment finishes (cloud-init installs it).

@description('Short name used for the VM and its resources.')
@minLength(2)
@maxLength(20)
param name string = 'bunker'

@description('Azure region. Defaults to the resource group\'s region.')
param location string = resourceGroup().location

@description('DNS label: Bunker will be at https://<label>.<region>.cloudapp.azure.com. Must be unique in the region.')
@minLength(3)
@maxLength(63)
param dnsLabel string = 'bunker-${uniqueString(resourceGroup().id)}'

@description('VM size. B2s (2 vCPU, 4 GiB) comfortably serves a few thousand users; B1ms is fine for trying it out.')
@allowed([
  'Standard_B1ms'
  'Standard_B2s'
  'Standard_B2ms'
  'Standard_D2s_v5'
  'Standard_D4s_v5'
])
param vmSize string = 'Standard_B2s'

@description('Size of the data disk for Bunker, in GiB.')
@minValue(8)
@maxValue(4095)
param dataDiskSizeGiB int = 32

@description('Admin user name on the VM.')
param adminUsername string = 'bunkeradmin'

@description('Sign-in method for the VM admin user.')
@allowed([
  'password'
  'sshPublicKey'
])
param authenticationType string = 'password'

@description('Admin password, or an SSH public key (ssh-ed25519 / ssh-rsa ...) when authenticationType is sshPublicKey.')
@secure()
param adminPasswordOrKey string

@description('Open SSH (port 22) to the internet. Off by default; use Azure Bastion, serial console or Run Command instead.')
param allowSsh bool = false

@description('One-time code for the first-run setup page. Leave the generated value unless you want your own.')
@minLength(6)
param setupCode string = toUpper(substring(replace(newGuid(), '-', ''), 0, 10))

@description('Bunker release to install, e.g. v1.2.0, or latest.')
param bunkerVersion string = 'latest'

var host = '${dnsLabel}.${location}.cloudapp.azure.com'
var url = 'https://${host}'
var cloudInit = replace(replace(replace(loadTextContent('cloud-init.yaml'), '__BUNKER_HOST__', host), '__BUNKER_SETUP_CODE__', setupCode), '__BUNKER_VERSION__', bunkerVersion)
var linuxConfiguration = {
  disablePasswordAuthentication: true
  ssh: {
    publicKeys: [
      {
        path: '/home/${adminUsername}/.ssh/authorized_keys'
        keyData: adminPasswordOrKey
      }
    ]
  }
}
var webRules = [
  {
    name: 'allow-http'
    properties: {
      priority: 100
      direction: 'Inbound'
      access: 'Allow'
      protocol: 'Tcp'
      sourceAddressPrefix: 'Internet'
      sourcePortRange: '*'
      destinationAddressPrefix: '*'
      destinationPortRange: '80'
    }
  }
  {
    name: 'allow-https'
    properties: {
      priority: 110
      direction: 'Inbound'
      access: 'Allow'
      protocol: '*' // TCP + UDP for HTTP/3
      sourceAddressPrefix: 'Internet'
      sourcePortRange: '*'
      destinationAddressPrefix: '*'
      destinationPortRange: '443'
    }
  }
]
var sshRule = [
  {
    name: 'allow-ssh'
    properties: {
      priority: 120
      direction: 'Inbound'
      access: 'Allow'
      protocol: 'Tcp'
      sourceAddressPrefix: 'Internet'
      sourcePortRange: '*'
      destinationAddressPrefix: '*'
      destinationPortRange: '22'
    }
  }
]

resource nsg 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  name: '${name}-nsg'
  location: location
  properties: {
    securityRules: allowSsh ? concat(webRules, sshRule) : webRules
  }
}

resource vnet 'Microsoft.Network/virtualNetworks@2023-11-01' = {
  name: '${name}-vnet'
  location: location
  properties: {
    addressSpace: { addressPrefixes: ['10.20.0.0/24'] }
    subnets: [
      {
        name: 'default'
        properties: {
          addressPrefix: '10.20.0.0/26'
          networkSecurityGroup: { id: nsg.id }
        }
      }
    ]
  }
}

resource pip 'Microsoft.Network/publicIPAddresses@2023-11-01' = {
  name: '${name}-ip'
  location: location
  sku: { name: 'Standard' }
  properties: {
    publicIPAllocationMethod: 'Static'
    publicIPAddressVersion: 'IPv4'
    dnsSettings: { domainNameLabel: dnsLabel }
  }
}

resource nic 'Microsoft.Network/networkInterfaces@2023-11-01' = {
  name: '${name}-nic'
  location: location
  properties: {
    ipConfigurations: [
      {
        name: 'ipconfig1'
        properties: {
          privateIPAllocationMethod: 'Dynamic'
          subnet: { id: vnet.properties.subnets[0].id }
          publicIPAddress: { id: pip.id }
        }
      }
    ]
  }
}

resource vm 'Microsoft.Compute/virtualMachines@2024-03-01' = {
  name: name
  location: location
  identity: { type: 'SystemAssigned' } // ready for backups to Blob storage
  properties: {
    hardwareProfile: { vmSize: vmSize }
    osProfile: {
      computerName: name
      adminUsername: adminUsername
      adminPassword: authenticationType == 'password' ? adminPasswordOrKey : null
      linuxConfiguration: authenticationType == 'password' ? null : linuxConfiguration
      customData: base64(cloudInit)
    }
    storageProfile: {
      imageReference: {
        publisher: 'Canonical'
        offer: 'ubuntu-24_04-lts'
        sku: 'server'
        version: 'latest'
      }
      osDisk: {
        name: '${name}-osdisk'
        createOption: 'FromImage'
        managedDisk: { storageAccountType: 'StandardSSD_LRS' }
        deleteOption: 'Delete'
      }
      dataDisks: [
        {
          name: '${name}-data'
          lun: 0
          createOption: 'Empty'
          diskSizeGB: dataDiskSizeGiB
          caching: 'ReadWrite'
          managedDisk: { storageAccountType: 'StandardSSD_LRS' }
          deleteOption: 'Detach' // keep Bunker's data if the VM is deleted
        }
      ]
    }
    networkProfile: {
      networkInterfaces: [{ id: nic.id, properties: { deleteOption: 'Delete' } }]
    }
    securityProfile: {
      securityType: 'TrustedLaunch'
      uefiSettings: { secureBootEnabled: true, vTpmEnabled: true }
    }
    diagnosticsProfile: {
      bootDiagnostics: { enabled: true } // serial console + boot log in the portal
    }
  }
}

@description('Bunker\'s public URL.')
output url string = url

@description('Open this page and enter the setup code to connect Microsoft Entra ID (ready a few minutes after deployment).')
output setupUrl string = '${url}/setup'

@description('One-time setup code.')
output setupCode string = setupCode

@description('Redirect URI to register on the Entra app registration.')
output entraRedirectUri string = '${url}/auth/callback'

@description('SSH command (when allowSsh is on).')
output ssh string = 'ssh ${adminUsername}@${host}'
