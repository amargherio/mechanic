ARG RUNTIME_IMAGE=mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0-nonroot
ARG BIN_PATH=./mechanic

FROM $RUNTIME_IMAGE
USER root
RUN tdnf update \
    && tdnf upgrade -y \
    && tdnf clean all \
USER nonroot

COPY $BIN_PATH /usr/local/bin/mechanic

CMD ["mechanic"]
