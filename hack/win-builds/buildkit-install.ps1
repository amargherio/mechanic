$version = "v0.31.2"
$arch = "amd64"
curl.exe -LO https://github.com/moby/buildkit/releases/download/$version/buildkit-$version.windows-$arch.tar.gz
          
New-Item -ItemType Directory -Path .\buildkit
tar.exe -xvf buildkit-$version.windows-$arch.tar.gz -C .\buildkit
Copy-Item -Path .\buildkit\bin -Destination $env:ProgramFiles\buildkit -Recurse -Force
Remove-Item -Path .\buildkit -Recurse -Force
          
$Path = [Environment]::GetEnvironmentVariable("PATH", "Machine") + [IO.Path]::PathSeparator + "$env:ProgramFiles\buildkit"
[Environment]::SetEnvironmentVariable("PATH", $Path, "Machine")
$Env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User")
          
buildkitd.exe `
  --register-service `
  --service-name buildkitd `
  --containerd-cni-config-path="C:\Program Files\containerd\cni\conf\10-containerd-nat.conf" `
  --containerd-cni-binary-dir="C:\Program Files\containerd\cni\bin" `
  --debug `
  --log-file="C:\Windows\Temp\buildkitd.log"
          
buildctl debug info