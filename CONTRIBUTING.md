# Contributing to mechanic

There's a few different ways to contribute to the mechanic project.

## Documentation work

If you're interested in helping to improve the documentation, you can check for potential open issues by filtering on
the `documentation` label. If you don't see an issue that you think should be addressed, feel free to open a new issue
with the `documentation` label and a description of what you'd like to see updated.

If that's something you're interested in working on, feel free to submit a PR with the changes.

## Code contributions

Code contributions should be based on open issues or feature requests. For new features or functionality, please open an issue
so that the maintainers can review and discuss the proposed changes.

### Dev environment setup

The dev environment requirements for mechanic are pretty minimal. Mechanic depends on the following tools:

- A valid Go installation (1.22 is the current version used)
- Docker or another container runtime for building images

In addition to the two hard dependencies, optional tools that can be helpful are:

- [Just](https://github.com/casey/just) for running tasks (optional, quality of life addition)
- [Kustomize](https://kustomize.io/) for managing Kubernetes manifests and YAML generation
- [Mockgen](https://github.com/uber-go/mock) for generating mocks for testing (required if you're adding code that requires mock updates)
