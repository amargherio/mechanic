# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2025.1] - 2025-01-10

### <!-- 7 -->⚙️ Miscellaneous Tasks
- Bumping the image version in yamls for 2025.1

## [1.0.0] - 2024-12-27

### <!-- 0 -->🚀 Features
- Adding support for customized drain conditions (#20)
- Opentelemtry tracing implementation
- Updated observability and improved logging (#36)

### <!-- 1 -->🐛 Bug Fixes
- Updated build components to handle distroless vs regular images
- Updating UncordonNode to correctly pull the values from the context
- Fixed a bad reference to the ContextValues struct when extracting it from the context in a function

### <!-- 2 -->🚜 Refactor
- Updating the container base runtime images to use azure linux 3.0

### <!-- 7 -->⚙️ Miscellaneous Tasks
- Updating project-level docs
- Working through changes to goreleaser config
- Justfile updates to break out local commands from universal ones
- Adding multiplatform support for Linux and Windows and building public images
- Updating env vars in the build action
- More workflow updates
- Actions workflow updates for goreleaser to build binaries
- Removing a flag from the goreleaser build in the image build workflow
- Removing bad arg from goreleaser
- Renaming mockfile so it's excluded from builds
- More build work
- More work on the image builder workflow
- Updating tracer package to be correct for mechanic
- Fixing env var definitions in YAML
- Pipeline variable fixes
- Variable fixes
- Disabling CI runs when the only changes are in the .github dir
- Syntax fixes in the image build workflow and windows buildfile updates
- Fixing tag references in the image build workflow
- Bad build-arg flag
- Trying to stop the workflows from running when changing .github files
- Changing the trigger events to only trigger on changes to source directories
- Adding build steps for uploading and downloading the built binaries for use across the image build pipeline
- Fixing typo in push trigger
- Testing artifact download and extract
- Fixing podman manifest push for multi-arch linux images
- Added cliff.toml back as tracked file
- Re-enabling the build stage for windows images
- Updating arg placement in windows dockerfile
- Windows image builds (#30)
- Windows 2019 and 2022 YAML updates (#37)
- Increasing golangci-lint timeout
- Fixing workflow triggers
- Generating v1.0.0 release notes
- Fixing the pathing for the compiled arm64 binary in linux multi-arch images
- Bumping the image tag in the kustomize manifests
- Adding in logic to pull the artifact path from goreleaser output. closes #40
- YAML updates for v1.0.0 image versions
- YAML updates for v1.0.0 image versions

## [0.2.0] - 2024-09-24

### <!-- 0 -->🚀 Features
- Emit kubernetes events for node operations
- Add logic to sync the node state with mechanic internal state on start

### <!-- 1 -->🐛 Bug Fixes
- Fixing test failures related to changes to context values
- Fixing a missed logger call in config
- Imds updates to fix document incarnation parsing
- Updates to DocumentIncarnation type in ScheduledEventResponse
- Corrected error when casting Resources JSON array to []string from []interface{}
- Duration was being incorrectly cast as an int - needed float64
- Fixed cordon labeling logic bug and updating drain to address a segfault in the drain helper calls
- Changing how appstate is used and updated
- Updating log messaging to include state and reduce volume
- Updated IMDS logic for retries on EOF and EOF handling
- Resolved duplicate node issue in node tests
- Updating justfile to fix syntax errors
- Added a call to get the node object before performing the cordon validation so we don't work on outdated objects

### <!-- 2 -->🚜 Refactor
- Adding in app state handling to reduce unnecessary kubernetes API calls
- Pulling app state and context values out into correct packages and updating the cordon/uncordon logic
- Updating IMDS components to return only errors and work to update app state with shouldDrain logic
- Reworked node update logic and added additional logging/handling with appstate
- Adding better handling for parsing events in the IMDS response
- Split logic for checking if uncordon is needed and if there's drainable node conditions
- Removed appstate sync function to prevent circular imports
- Moved the node cordon check and node condition check into the node package
- State locking and sync across update calls
- Fixing some of the cordon validation logic
- Changed mutex unlock defer to include logging

### <!-- 7 -->⚙️ Miscellaneous Tasks
- Fixing accidental image changes
- Updating dependency versions
- Fixing golangci-lint failures
- Scaffolding out kustomize structure for daemonset builds
- Finishing initial work for kustomize deploy
- Fixing linting checks
- Fixing lint findings with mutex added to app state
- Added command to dockerfile for updating image packages prior to build completing

## [0.1.2] - 2024-07-15

### <!-- 1 -->🐛 Bug Fixes
- Added missing logic to label nodes when we cordon them

### <!-- 7 -->⚙️ Miscellaneous Tasks
- Repo maintenance
- Mistake in dependabot config

## [0.1.1] - 2024-07-12

### <!-- 1 -->🐛 Bug Fixes
- Added handling for freeze events since they may not be live migrations all the time
- Minor fixes in IMDS and related test logging
- Typo for golangci-lint in the workflow

### <!-- 6 -->🧪 Testing
- Adding tests for imds and node packages

### <!-- 7 -->⚙️ Miscellaneous Tasks
- Adding linting config
- Updating deploy yamls to move mechanic DS and service account into their own namespace

[2025.1]: https://github.com///compare/v1.0.0..v2025.1
[1.0.0]: https://github.com///compare/v0.2.0..v1.0.0
[0.2.0]: https://github.com///compare/v0.1.2..v0.2.0
[0.1.2]: https://github.com///compare/v0.1.1..v0.1.2
[0.1.1]: https://github.com///compare/v0.1.0..v0.1.1

<!-- generated by git-cliff -->
