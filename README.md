# Mechanic

[![Go Report Card](https://goreportcard.com/badge/github.com/amargherio/mechanic)](https://goreportcard.com/report/github.com/amargherio/mechanic)
![License](https://img.shields.io/github/license/amargherio/mechanic)
![Go version](https://img.shields.io/github/go-mod/go-version/amargherio/mechanic)

> Working under the hood to stop disruptions to your AKS nodes

## Description

**mechanic** is a tool for AKS clusters that helps mitigate the impact from platform maintenance events
or other node-impactful conditions where the node may not be cordoned and/or drained. Its primary focus
is preventing application impacts from maintenance events that require node reboots or live migrations
without moving pods unnecessarily or causing application downtime.

It does this by monitoring node conditions and, when a maintenance event is indicated, querying the Instance Metadata Service
for maintenance event details. If the event is deemed impactful to the node, it will cordon and drain the node to ensure
pods are rescheduled to other nodes before the maintenance event occurs.

## What's the best way to use this?

The best combination of functionality would be using this alongside Cluster Autoscaler. The built-in node problem detector
implementation used by AKS will manage the `VMEventScheduled` and other node condition which triggers this drain functionality.

As the pods are drained from the node, without Cluster Autoscaler the cluster could exhaust available compute resources;
using CAS or [Node Autoprovisioning](https://learn.microsoft.com/en-us/azure/aks/node-autoprovision?tabs=azure-cli) would
ensure that the cluster can scale to meet the demands of the pods being rescheduled.

### Installing mechanic in a cluster

> _tl;dr_ - `kubectl apply -f deploy/static/mechanic.yaml` for the default configuration and latest prod image.

The recommended way to run mechanic is through a DaemonSet - this ensures that each node in the cluster has a monitor that
can coordinate cordon and drain operations. There are some limitations at this time - namely:

- No support for Windows nodes running on ARM64 SKUs.

Mechanic is offered as a base set of YAMLs that can be applied to your cluster through the use of [kustomize](https://kustomize.io/).
For details on generating valid YAML to install the DaemonSet, see the [installation](./docs/install.md) guide.

There are some caveats and items worth noting:

- The DaemonSet is deployed in a custom `mechanic` namespace. This is to ensure that the DaemonSet can be managed independently
  of other resources in the cluster.
- The Kustomize base offers a prebuilt image hosted in the GitHub Container Registry packages of this repository. If you choose,
  you can build your own image or pull the image from the GitHub Container Registry for this project and push it into
  your own registry. Once the image is in a registry, you can create a patch to have Kustomize update the image URL.
- All images use a base container image of Azure Linux.

## How does it work?

**mechanic** runs as a DaemonSet in your cluster. Each daemon pod monitors node updates and, for each update, checks the
node conditions for any `VMEventScheduled` or other potentially impacting events. If any of the node conditions are confirmed
as present, it queries the [Instance Metadata Service](https://learn.microsoft.com/en-us/azure/virtual-machines/instance-metadata-service?tabs=linux)
for maintenance information (in the event of a `VMEventScheduled` condition) or proceedes with cordon and drain
operations based on the configuration provided in the ConfigMap created for use.

If the maintenance event or node condition is deemed impactful, it will cordon the node and begin draining pods to other nodes in the cluster.
During the drain flow, a label is added to the node (`mechanic.cordoned`) indicating that it was cordoned by mechanic. If the daemon pod is restarted,
it will check for this label and use it as an input on whether to uncordon the node if the `VMEventScheduled` or other condition is
no longer present.

## I'm interested in contributing

Great! We're always looking for contributors to help improve the project. If you're interested in contributing, please see
the [contributing docs](./CONTRIBUTING.md) for more information on how to get started.
