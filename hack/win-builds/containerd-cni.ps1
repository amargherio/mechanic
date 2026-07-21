$networkName = 'nat'
$natInfo = Get-HnsNetwork -ErrorAction Ignore | Where-Object { $_.Name -eq $networkName }
if ($null -eq $natInfo) {
    throw "NAT network not found, check if you enabled containers, Hyper-V features, and restarted the machine"
}
          
$gateway = $natInfo.Subnets[0].GatewayAddress
$subnet = $natInfo.Subnets[0].AddressPrefix
          
$cniConfPath = "$env:ProgramFiles\containerd\cni\conf\10-containerd-nat.conf"
$cniBinDir = "$env:ProgramFiles\containerd\cni\bin"
$cniPluginVersion = "0.3.3"
$cniVersion = "1.0.0"
          
mkdir $cniBinDir -Force
curl.exe -LO https://github.com/microsoft/windows-container-networking/releases/download/v$cniPluginVersion/windows-container-networking-cni-amd64-v$cniPluginVersion.zip
tar xvf windows-container-networking-cni-amd64-v$cniPluginVersion.zip -C $cniBinDir
          
$natConfig = @"
{
  "cniVersion": "$cniVersion",
  "name": "$networkName",
  "type": "nat",
  "master": "Ethernet",
  "ipam": {
    "subnet": "$subnet",
    "routes": [
      { 
        "gateway": "$gateway"
      }
    ]
  },
  "capabilities": {
      "portMappings": true,
      "dns": true
  }
}
"@
Set-Content -Path $cniConfPath -Value $natConfig