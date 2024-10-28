# Installing Mechanic

The recommended way to run mechanic is through a DaemonSet - this ensures that each node in the cluster has a monitor that
can coordinate cordon and drain operations. There are some limitations at this time - namely:

- ARM support is available for Linux nodes only.
- Windows images are available for use on Windows nodes but have not been thoroughly tested.

Mechanic provides pre-built images via the GitHub Container Registry. However, if you prefer or need to build the mechanic images from scratch to meet specific security requirements or standards, the Kustomize base in the `deploy` directory can be used for this purpose. This flexibility allows you to customize the mechanic install to fit your runtime environment and adhere to your security policies.

## Prerequisites

- A working install of the standalone binary for [kustomize](https://kustomize.io/).
- A working container runtime environment that can build and push images to a registry.

## Installation

There are two installation paths:

- If you're OK using the prebuilt images in the GitHub registry and don't need to make any changes to the YAML found in the Kustomize base (located in the `deploy` directory), you can use the following command to install mechanic:

  ```shell
  kubectl apply -f deploy/base/mechanic.ds.yaml
  ```

- If you need to customize the YAML or build the container image from scratch, follow the steps below.

### (optional) Building the container image

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
   bases:
     - ../../base
   ```

   The YAML above shows that it's using the contents of the `base` directory as it's starting point. If you need to make changes to the image used in the deployment (such as pulling from a different registry), you can add the following block at the bottom, filling in the details as appropriate:

   ```yaml
    images:
      - name: ghcr.io/amargherio/mechanic
        newName: <your-image-url>
        newTag: <your-image-tag>
    ```

   You can also add additional YAMLs, such as a ConfigMap or Secret, to the overlay directory and reference them in the `kustomization.yaml` file. This allows you to override the base configuration used by mechanic to something more appropriate for your runtime environment and needs.

To complete the deployment, you can use the following one liner from the repository root directory: `kustomize build deploy/overlays/dev | kubectl apply -f -`.

You can view the generated YAML without applying it to the cluster by running `kustomize build deploy/overlays/dev`.
