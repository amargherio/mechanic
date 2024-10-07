ARG RUNTIME_IMAGE=mcr.microsoft.com/windows/nanoserver:1809
ARG BIN_PATH=./mechanic.exe

FROM $RUNTIME_IMAGE

COPY mechanic.exe $BIN_PATH

CMD ["mechanic.exe"]