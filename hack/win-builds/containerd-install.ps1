$Version = "1.7.23"
$Arch = "amd64"
          
curl.exe -LO https://github.com/containerd/containerd/releases/download/v$Version/containerd-$Version-windows-$Arch.tar.gz
New-Item -ItemType Directory -Path .\containerd
tar.exe -xvf containerd-$Version-windows-$Arch.tar.gz -C .\containerd
          
Copy-Item -Path .\containerd\bin -Destination $env:ProgramFiles\containerd -Recurse -Force
Remove-Item -Path .\containerd -Recurse -Force
          
$Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + [IO.Path]::PathSeparator + "$env:ProgramFiles\containerd"
[Environment]::SetEnvironmentVariable("Path", $Path, "Machine")
$Env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User")
          
containerd.exe config default | Out-File $Env:ProgramFiles\containerd\config.toml -Encoding ascii
Get-Content $Env:ProgramFiles\containerd\config.toml

containerd.exe --register-service
Start-Service containerd
          
containerd.exe --version