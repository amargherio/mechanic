ARG RUNTIME_IMAGE=mcr.microsoft.com/windows/nanoserver:1809
FROM $RUNTIME_IMAGE

ARG BIN_PATH=./mechanic.exe

COPY mechanic.exe $BIN_PATH

CMD ["mechanic.exe"]