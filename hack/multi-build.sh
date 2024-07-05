#!/bin/env bash

# This script is used to build mechanic with a range of different base images.
URL_BASE="mcr.microsoft.com/cbl-mariner/"
IMAGES=(base/core distroless/debug distroless/minimal)
ACR_TAG="amargheriocss.azurecr.io/platform-apps/mechanic"
VERSION_TAG="0.1.0-2"

for IMAGE in "${IMAGES[@]}"; do
    echo "Building mechanic using base image $URLBASE$IMAGE"
    # Generate a tag suffix based on the value of IMAGE
    TAG_SUFFIX=""
   
    case $IMAGE in
        "base/core")
            TAG_SUFFIX="nonroot"
            ;;
        "distroless/debug")
            TAG_SUFFIX="debug-nonroot"
            ;;
        "distroless/minimal")
            TAG_SUFFIX="distroless-nonroot"
            ;;
    esac
    podman build -t "${ACR_TAG}:${VERSION_TAG}-${TAG_SUFFIX}" --build-arg BASE_IMAGE=$URL_BASE$IMAGE -f ./build/Dockerfile .
done