ARG BUILD_IMAGE=golang:1.22-bookworm
ARG RUNTIME_IMAGE=mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0-nonroot

FROM $BUILD_IMAGE as builder

WORKDIR /usr/src/mechanic
COPY . .

RUN go build -o mechanic ./cmd/mechanic

FROM $RUNTIME_IMAGE

# Use TDNF to update the packages to latest versions
RUN tdnf update -y \
    && tdnf upgrade -y \
    && tdnf clean all

COPY --from=builder /usr/src/mechanic/mechanic /usr/local/bin/mechanic

CMD ["mechanic"]
