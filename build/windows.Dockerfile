ARG RUNTIME_IMAGE=mcr.microsoft.com/windows/nanoserver:1809
FROM $RUNTIME_IMAGE

ARG BIN_PATH=./mechanic.exe

RUN mkdir C:\\mechanic

ENV PATH="$WindowsPATH;C:\\mechanic"

COPY $BIN_PATH C:\\mechanic\\mechanic.exe

CMD [mechanic.exe]