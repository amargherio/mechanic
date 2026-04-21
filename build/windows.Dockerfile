ARG RUNTIME_IMAGE=mcr.microsoft.com/windows/nanoserver:ltsc2022
FROM ${RUNTIME_IMAGE}

ARG BIN_PATH=./mechanic.exe

WORKDIR C:/mechanic
ENV PATH="C:/mechanic;${PATH}"
COPY ${BIN_PATH} C:/mechanic/mechanic.exe

CMD ["mechanic.exe"]