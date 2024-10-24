ARG RUNTIME_IMAGE=mcr.microsoft.com/windows/nanoserver:1809
FROM $RUNTIME_IMAGE

ARG BIN_PATH=./mechanic.exe

#SHELL ["pwsh", "-Command", "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue';"]

WORKDIR "C:\\"

#RUN New-Item -ItemType Directory -Path C:\mechanic; [Environment]::SetEnvironmentVariable('PATH', $env:PATH + ';C:\mechanic', [EnvironmentVariableTarget]::Machine)
RUN mkdir C:\\mechanic

COPY $BIN_PATH C:\mechanic\mechanic.exe

CMD ["mechanic.exe"]