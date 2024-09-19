default:
  just --list

test:
  go test ./...

build-release:
  goreleaser --rm-dist
  # build using goreleaser

build-local-containers:
  ./hack/local_build_call.sh

generate-release-notes:
  git cliff --bump -o CHANGELOG.md

test-and-lint: test
    golangci-lint run

 build-images repository tag:
    (which docker || which podman) || (echo "a valid container runtime install is required to build images" && exit 1)
    if [ command -v docker ]; then
      docker build -t {{repository}}/mechanic:{{tag}} . -f build/Dockerfile
    else
      podman build -t {{repository}}/mechanic:{{tag}} . -f build/Dockerfile
    fi

 apply env:
    (which kubectl && which kustomize) || (echo "kubectl and kustomize are required" && exit 1)
    kustomize build ./deploy/overlays/{{env}} | kubectl apply -f -