ARG BUILD_IMAGE=golang:1.22-bookworm
ARG RUNTIME_IMAGE=mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0-nonroot
ARG BIN_PATH=mechanic

FROM $BUILD_IMAGE as builder

RUN wget --quiet https://github.com/goreleaser/goreleaser/releases/download/v2.3.2/goreleaser_2.3.2_amd64.deb \
    && dpkg -i goreleaser_2.3.2_amd64.deb \
    && rm goreleaser_2.3.2_amd64.deb

WORKDIR /usr/src/mechanic
COPY . .

RUN goreleaser build --snapshot --clean

FROM $RUNTIME_IMAGE
# if the RUNTIME_IMAGE contains distroless, skip the following steps
USER root
RUN tdnf update -y \
    && tdnf upgrade -y \
    && tdnf clean all \
    && rm -rf /var/cache/tdnf
USER nonroot

COPY --from=builder "/usr/src/mechanic/${BIN_PATH}" /usr/local/bin/mechanic

CMD ["mechanic"]
