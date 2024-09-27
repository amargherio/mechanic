ARG RUNTIME_IMAGE=mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0-nonroot
ARG BIN_PATH=./mechanic

FROM $RUNTIME_IMAGE

COPY $BIN_PATH /usr/local/bin/mechanic

CMD ["mechanic"]