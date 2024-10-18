buildctl build --frontend dockerfile.v0 \
    --local context=. \
    --local dockerfile=.\build\windows.Dockerfile \
    --opt platform=windows/amd64 \
    --build-arg BIN_PATH=dist/mechanic_windows_amd64*/mechanic \
    --build-arg RUNTIME_IMAGE=${{ env.windows_runtime_image }} \
    --output type=image,name=${{ env.target_container_registry}}/mechanic:${{ env.target_container_tag }}-windows

buildctl build --frontend dockerfile.v0 \
    --local context=. \
    --local dockerfile=.\build\windows.Dockerfile \
    --opt platform=windows/arm64 \
    --opt build-arg:BIN_PATH=dist/mechanic_windows_arm64/mechanic \
    --opt build-arg:RUNTIME_IMAGE=${{ env.windows_runtime_image }} \
    --output type=image,name=${{ env.target_container_registry}}/mechanic:${{ env.target_container_tag }}-windows