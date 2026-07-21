ARG RUNTIME_IMAGE=mcr.microsoft.com/azurelinux/distroless/minimal:3.0
FROM $RUNTIME_IMAGE

ARG BIN_PATH=./mechanic

COPY $BIN_PATH /usr/local/bin/mechanic

USER nonroot

CMD ["mechanic"]