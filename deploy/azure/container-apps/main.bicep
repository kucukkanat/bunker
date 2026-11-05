// Bunker on Azure Container Apps -- EVALUATION ONLY.
//
// Data lives on the replica's local ephemeral disk (EmptyDir), so it is LOST
// whenever the replica restarts, moves or a new revision deploys. Container
// Apps only offers Azure Files (SMB/NFS) for persistence, and SQLite on a
// network filesystem breaks ("database is locked"). For real use, deploy the
// VM template in ../main.bicep (the "Deploy to Azure" button in DEPLOY.md).
//
//   az group create -n bunker-eval -l westeurope
//   az deployment group create -g bunker-eval -f deploy/azure/container-apps/main.bicep
//
// Compiled: az bicep build -f main.bicep --outfile azuredeploy.json
// Outputs: the public URL, the setup page and the one-time setup code.

@description('Name for the Container App (lowercase letters, numbers and dashes). Becomes part of the URL.')
@minLength(2)
@maxLength(32)
param appName string = 'bunker'

@description('Azure region. Defaults to the resource group\'s region.')
param location string = resourceGroup().location

@description('Bunker container image.')
param image string = 'ghcr.io/kucukkanat/bunker:latest'

@description('One-time code for the first-run setup page. Leave the generated value unless you want your own.')
@minLength(6)
param setupCode string = toUpper(substring(replace(newGuid(), '-', ''), 0, 10))

@description('vCPU and memory for the container.')
@allowed([
  '0.5 vCPU / 1 GiB'
  '1 vCPU / 2 GiB'
  '2 vCPU / 4 GiB'
])
param size string = '0.5 vCPU / 1 GiB'

@description('Optional custom public URL (e.g. https://bunker.contoso.com). Leave empty to use the Container Apps URL.')
param publicUrl string = ''

var sizes = {
  '0.5 vCPU / 1 GiB': { cpu: json('0.5'), memory: '1Gi' }
  '1 vCPU / 2 GiB': { cpu: json('1.0'), memory: '2Gi' }
  '2 vCPU / 4 GiB': { cpu: json('2.0'), memory: '4Gi' }
}
resource logs 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: '${appName}-logs'
  location: location
  properties: {
    sku: { name: 'PerGB2018' }
    retentionInDays: 30
  }
}

resource env 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: '${appName}-env'
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logs.properties.customerId
        sharedKey: logs.listKeys().primarySharedKey
      }
    }
    workloadProfiles: [
      { name: 'Consumption', workloadProfileType: 'Consumption' }
    ]
  }
}

var url = empty(publicUrl) ? 'https://${appName}.${env.properties.defaultDomain}' : publicUrl

resource app 'Microsoft.App/containerApps@2024-03-01' = {
  name: appName
  location: location
  properties: {
    environmentId: env.id
    workloadProfileName: 'Consumption'
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: 8080
        transport: 'auto'
        allowInsecure: false
      }
      secrets: [
        // The setup code is one-time and deliberately shown in the outputs.
        #disable-next-line use-secure-value-for-secure-inputs
        { name: 'setup-code', value: setupCode }
      ]
    }
    template: {
      containers: [
        {
          name: 'bunker'
          image: image
          resources: sizes[size]
          env: [
            { name: 'BUNKER_URL', value: url }
            { name: 'BUNKER_DATA_DIR', value: '/data' }
            { name: 'BUNKER_TRUST_PROXY', value: 'true' }
            { name: 'BUNKER_SETUP_CODE', secretRef: 'setup-code' }
          ]
          volumeMounts: [
            { volumeName: 'data', mountPath: '/data' }
          ]
          probes: [
            {
              type: 'Startup'
              httpGet: { path: '/healthz', port: 8080 }
              periodSeconds: 5
              failureThreshold: 30
            }
            {
              type: 'Liveness'
              httpGet: { path: '/healthz', port: 8080 }
              periodSeconds: 30
              failureThreshold: 3
            }
          ]
        }
      ]
      // SQLite: exactly one replica, always on.
      scale: {
        minReplicas: 1
        maxReplicas: 1
      }
      volumes: [
        {
          // Local, ephemeral disk. NOT Azure Files: SQLite over SMB fails
          // with "database is locked".
          name: 'data'
          storageType: 'EmptyDir'
        }
      ]
    }
  }
}

@description('Bunker\'s public URL.')
output url string = url

@description('Open this page and enter the setup code to connect Microsoft Entra ID.')
output setupUrl string = '${url}/setup'

@description('One-time setup code.')
output setupCode string = setupCode

@description('Redirect URI to register on the Entra app registration.')
output entraRedirectUri string = '${url}/auth/callback'
