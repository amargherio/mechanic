import? './release.just'
import? './local.just'

default:
  just --list

test:
  go test ./...

test-and-lint: test
  golangci-lint run

update-mocks:
  mockgen -destination pkg/imds/imds_mock_test.go -package imds -source pkg/imds/imds.go IMDS

apply env:
  (which kubectl && which kustomize) || (echo "kubectl and kustomize are required" && exit 1)
  kustomize build ./deploy/overlays/{{env}} | kubectl apply -f -