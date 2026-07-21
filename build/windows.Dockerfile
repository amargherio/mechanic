ARG RUNTIME_IMAGE=mcr.microsoft.com/windows/nanoserver:ltsc2022
FROM $RUNTIME_IMAGE

ARG BIN_PATH=./mechanic.exe

COPY $BIN_PATH C:/mechanic/mechanic.exe

USER ContainerUser

CMD ["C:\\mechanic\\mechanic.exe"]