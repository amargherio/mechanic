default:
  just --list

test:
  go test ./...

test-and-lint: test
  golangci-lint run

update-mocks:
  mockgen -destination pkg/imds/mock_imds.go -package imds -source pkg/imds/imds.go IMDS

build-release:
  goreleaser --rm-dist

build-local-containers:
  ./hack/local_build_call.sh

generate-release-notes:
  git cliff --bump -o CHANGELOG.md

build-image repository tag:
  (which docker || which podman) || (echo "a valid container runtime install is required to build images" && exit 1)
  if `command -v docker > /dev/null 2>&1` {
  echo "using docker"
  docker build -t {{repository}}/mechanic:{{tag}} . -f build/Dockerfile
  } else
  echo "using podman"
  podman build -t {{repository}}/mechanic:{{tag}} . -f build/Dockerfile
  }

apply env:
  (which kubectl && which kustomize) || (echo "kubectl and kustomize are required" && exit 1)
  kustomize build ./deploy/overlays/{{env}} | kubectl apply -f -