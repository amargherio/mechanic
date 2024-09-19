# Installing Mechanic

The recommended way to run mechanic is through a DaemonSet - this ensures that each node in the cluster has a monitor that
can coordinate cordon and drain operations. There are some limitations at this time - namely:

- No ARM nodes are supported. The container images for mechanic are built for amd64 architectures.
- No Windows node support. The container images target a Linux environment.

Mechanic also doesn't offer any pre-built images or generic YAML; the `deploy` directory contains the base of a Kustomize
folder structure that you can extend to fit your runtime environment and customize the mechanic install to fit your needs. The pre-built
images aren't provided out of an awareness that the images may need to be customized to work in your environment and meet your
security standards.

## Prerequisites

- A working install of the standalone binary for [kustomize](https://kustomize.io/).
- A working container runtime environment that can build and push images to a registry.

## Installation

The installation is a two step process as it is outlined here; the first step is to build the container image and push it to a registry, after which
kustomize can be used to generate YAML for install into a Kubernetes cluster.

### Building the container image

The `Dockerfile` located in the build directory uses a multi-stage build to build the mechanic binary from source and then copy it into a
container image. The build image uses Go 1.22 image from the official Go repository and allows for user input via build arguments to
specify the runtime image. If no runtime image is provided, the default is `mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0-nonroot`.

Building the image can be done via the Justfile or by running the build command manually:

```shell
just build

# or, if you prefer calling the build command directly
docker build -t mechanic:latest -f build/Dockerfile .
```
Once built, you can update the tag on the image via `docker tag mechanic:latest <registry>/<namespace>/mechanic:latest` and push the image to your registry.

### Generating YAML via Kustomize

Kustomize is a tool with a lot of capabilities and an explanation of everything it offers is outside the scope of this document. The `deploy` directory
contains a `base` subfolder that contains the base `Kustomization` file and a `mechanic.yaml` file that contains the base implementation of
all cluster resources needed to run mechanic.

You'll leverage the concept of Kustomize overlays to customize the base implementation to fit your environment.

1. Within the `deploy` directory, create a new directory for your overlay. In this example, we're configuring our overlay for a development environment:

```shell
mkdir -p deploy/overlays/dev
```

2. Create a `kustomization.yaml` file in the overlay directory and reference the base directory:

```yaml
resources:
  - ../../base

patchesStrategicMerge:
  - image.yaml
```

The YAML above shows that it's using the contents of the `base` directory as it's starting point, and it's applying a patch to that
starting point from a YAML file named `image.yaml`.

`image.yaml` is updating the image URL and tag used by the daemonset from it's placeholder value to a valid URL that can be pulled into
a cluster:

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: mechanic
  namespace: mechanic
spec:
  template:
    spec:
      containers:
        - name: mechanic
          image: <your-image-url>:<your-image-tag>
```

To complete the deployment, you can use the following one liner from the repository root directory: `kustomize build deploy/overlays/dev | kubectl apply -f -`.

You can view the generated YAML without applying it to the cluster by running `kustomize build deploy/overlays/dev`.

As a final alternative, you can use the Justfile to generate and apply the YAML as well. The Justfile depends on `kubectl` being available in your PATH and the correct cluster set in the kubeconfig:

```shell
just apply
```