test:
  go test ./...

build-release:
  goreleaser --rm-dist
  # build using goreleaser

build-local-containers:
  ./hack/local_build_call.sh

generate-release-notes:
  git cliff --bump -o CHANGELOG.md