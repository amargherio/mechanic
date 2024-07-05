FROM golang:1.22-bookworm as builder

WORKDIR /usr/src/mechanic
COPY . .

RUN go build -o mechanic ./cmd/mechanic

FROM mcr.microsoft.com/cbl-mariner/base/core:2.0

COPY --from=builder /usr/src/mechanic/mechanic /usr/local/bin/mechanic

CMD ["mechanic"]
