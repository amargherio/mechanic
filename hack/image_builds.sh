#!/usr/bin/env bash

BASE_IMAGES=("base/core:2.0-nonroot" "distroless/debug:2.0-nonroot" "distroless/minimal:2.0-nonroot")
IMAGE_URL_BASE="mcr.microsoft.com/cbl-mariner"
IS_DISTROLESS=false

# check if docker and podman are installed. if we have podman but not docker, alias docker to podman
if command -v podman &> /dev/null; then
  CONTAINER_TOOL=podman
elif command -v docker &> /dev/null; then
  CONTAINER_TOOL=docker
else
  echo "Neither docker nor podman are installed. Please install one of them."
  exit 1
fi

for IMAGE in "${BASE_IMAGES[@]}"; do
  SUFFIX=""
  # drop the 2.0-nonroot suffix from the image name
  IMAGE_NO_TAG=$(echo $IMAGE | cut -d':' -f1)

  case $IMAGE_NO_TAG in
    "distroless/debug")
      SUFFIX="-debug-nonroot"
      IS_DISTROLESS=true
      ;;
    "distroless/minimal")
      SUFFIX="-distroless-nonroot"
      IS_DISTROLESS=true
      ;;
    *)
      SUFFIX="-nonroot"
      IS_DISTROLESS=false
      ;;
  esac

  if [ "$IS_DISTROLESS" = true ]; then
    echo "Building mechanic with distroless base image $IMAGE"
    $CONTAINER_TOOL build -t "$TARGET_REGISTRY/mechanic:$APP_VERSION$SUFFIX" --build-arg RUNTIME_IMAGE="$IMAGE_URL_BASE/$IMAGE" -f ./build/distroless.Dockerfile .
      if [ $? -ne 0 ]; then
        echo "Failed to build with runtime base image $IMAGE_URL_BASE/$IMAGE"
        exit 1
      fi

      echo "Successfully built mechanic with runtime base image $IMAGE_URL_BASE/$IMAGE. Tagged as $TARGET_REGISTRY/mechanic:$APP_VERSION$SUFFIX"
  else
    $CONTAINER_TOOL build -t "$TARGET_REGISTRY/mechanic:$APP_VERSION$SUFFIX" --build-arg RUNTIME_IMAGE="$IMAGE_URL_BASE/$IMAGE" -f ./build/Dockerfile .
      if [ $? -ne 0 ]; then
        echo "Failed to build with runtime base image $IMAGE_URL_BASE/$IMAGE"
        exit 1
      fi
      echo "Successfully built mechanic with runtime base image $IMAGE_URL_BASE/$IMAGE. Tagged as $TARGET_REGISTRY/mechanic:$APP_VERSION$SUFFIX"
  fi
done

echo "All images successfully built"
