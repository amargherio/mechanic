$url = "https://api.github.com/repos/moby/buildkit/releases/latest"
$version = (Invoke-RestMethod -Uri $url -UseBasicParsing).tag_name
$arch = "amd4"
curl.exe -LO https://github.com/moby/buildkit/releases/download/$version/buildkit-$version.windows-$arch.tar.gz
          
mkdir .\buildkit
tar.exe -xvf buildkit-$version.windows-$arch.tar.gz -C .\buildkit
Copy-Item -Path .\buildkit\bin -Destination $env:ProgramFiles\buildkit -Recurse -Force
Delete-Item -Path .\buildkit -Recurse -Force
          
$Path = [Environment]::GetEnvironmentVariable("PATH", "Machine") + [IO.Path]::PathSeparator + "$env:ProgramFiles\buildkit"
[Environment]::SetEnvironmentVariable("PATH", $Path, "Machine")
$Env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User")
          
buildkitd.exe `
  --register-service `
  --service-name buildkitd `
  --containerd-cni-config-path="C:\Program Files\containerd\cni\conf\10-containerd-nat.conf" `
  --conatienrd-cni-binary-dir="C:\Program Files\containerd\cni\bin" `
  --debug `
  --log-file="C:\Windows\Temp\buildkitd.log"
          
buildctl debug info