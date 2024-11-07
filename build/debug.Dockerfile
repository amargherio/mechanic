FROM ubuntu:latest

RUN apt-get update && apt-get install -y curl \
    && curl -kLO https://go.dev/dl/go1.23.2.linux-amd64.tar.gz \
    && tar -C /usr/local -xzf go1.23.2.linux-amd64.tar.gz \
    && rm go1.23.2.linux-amd64.tar.gz \
    && export PATH=$PATH:/usr/local/go/bin \
    && go install github.com/go-delve/delve/cmd/dlv@latest

# Copy the build artifacts from the previous stage
ARG BIN_PATH=./mechanic

COPY $BIN_PATH /usr/local/bin/mechanic

CMD ["/root/go/bin/dlv", "exec", "/usr/local/bin/mechanic", "--headless", "--listen=:2345", "--api-version=2", "--accept-multiclient", "--log"]