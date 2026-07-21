FROM golang:1.26.5-bookworm AS builder

RUN go install github.com/goreleaser/goreleaser/v2@v2.17.0

WORKDIR /usr/src/mechanic
COPY . .

RUN goreleaser build --snapshot --clean --single-target --output /tmp/mechanic \
    && test -x /tmp/mechanic

FROM mcr.microsoft.com/azurelinux/base/core:3.0
USER root
RUN tdnf update -y \
    && tdnf distro-sync -y \
    && tdnf clean all \
    && rm -rf /var/cache/tdnf
USER nonroot

COPY --from=builder /tmp/mechanic /usr/local/bin/mechanic

CMD ["mechanic"]
