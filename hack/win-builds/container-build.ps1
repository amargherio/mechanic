[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BinPath,
    [string]$RuntimeImage = "mcr.microsoft.com/windows/nanoserver:ltsc2022",
    [string]$ImageName = "docker.io/library/mechanic:local-amd64",
    [string]$BuildctlPath = "C:\Program Files\buildkit\bin\buildctl.exe"
)

& $BuildctlPath build `
    --frontend dockerfile.v0 `
    --local context=. `
    --local dockerfile=.\build `
    --opt filename=windows.Dockerfile `
    --opt platform=windows/amd64 `
    --opt "build-arg:BIN_PATH=$BinPath" `
    --opt "build-arg:RUNTIME_IMAGE=$RuntimeImage" `
    --output "type=image,name=$ImageName,store=true"

if ($LASTEXITCODE -ne 0) {
    throw "buildctl build failed with exit code $LASTEXITCODE"
}