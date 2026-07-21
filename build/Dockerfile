ARG RUNTIME_IMAGE=mcr.microsoft.com/azurelinux/base/core:3.0
FROM $RUNTIME_IMAGE

ARG BIN_PATH=./mechanic

USER root
RUN tdnf update -y \
    && tdnf distro-sync -y \
    && tdnf clean all
USER nonroot

COPY $BIN_PATH /usr/local/bin/mechanic

CMD ["mechanic"]
