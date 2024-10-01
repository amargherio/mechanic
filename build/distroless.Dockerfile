ARG RUNTIME_IMAGE=mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0-nonroot
FROM $RUNTIME_IMAGE

ARG BIN_PATH=./mechanic

COPY $BIN_PATH /usr/local/bin/mechanic

CMD ["mechanic"]