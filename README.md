# Mechanic
[![Go Report Card](https://goreportcard.com/report/github.com/amargherio/mechanic)](https://goreportcard.com/report/github.com/amargherio/mechanic)
![License](https://img.shields.io/github/license/amargherio/mechanic)
![Go version](https://img.shields.io/github/go-mod/go-version/amargherio/mechanic)

> Working under the hood to stop disruptions to your AKS nodes

## Description

**mechanic** is a tool for AKS clusters that helps mitigate the impact from platform maintenance events. Its primary focus
is preventing application impacts from maintenance events that require node reboots or live migrations without moving pods
unnecessarily or causing application downtime.

It does this by monitoring node conditions and, when a maintenance event is indicated, querying the Instance Metadata Service
for maintenance event details. If the event is deemed impactful to the node, it will cordon and drain the node to ensure 
pods are rescheduled to other nodes before the maintenance event occurs.

## What's the best way to use this?

The best combination of functionality would be using this alongside Cluster Autoscaler. The built-in node problem detector
implementation used by AKS will manage the `VMEventScheduled` node condition which triggers this drain functionality.

As the pods are drained from the node, without Cluster Autoscaler the cluster could exhaust available compute resources;
using CAS or [Node Autoprovisioning](https://learn.microsoft.com/en-us/azure/aks/node-autoprovision?tabs=azure-cli) would 
ensure that the cluster can scale to meet the demands of the pods being rescheduled.

### Installing mechanic in a cluster

The recommended way to run mechanic is through a DaemonSet - this ensures that each node in the cluster has a monitor that
can coordinate cordon and drain operations. There are some limitations at this time - namely:

- No ARM nodes are supported. The container images for mechanic are built for amd64 architectures.
- No Windows node support. The container images target a Linux environment.

To install the DaemonSet in the cluster, you can run the following command:

```shell
kubectl apply -f https://raw.githubusercontent.com/amargherio/mechanic/main/deploy/mechanic.ds.yaml
```

There are some caveats and items worth noting:

- The DaemonSet is deployed in a custom `mechanic` namespace. This is to ensure that the DaemonSet can be managed independently
  of other resources in the cluster.
- The DaemonSet pulls the image present in the GitHub Container Registry for this repo. If you have pull restrictions, you need to
  make sure you've got the image pulled into a registry you're permitted to pull from.
- All images use a base container image of Azure Linux.

## How does it work?

**mechanic** runs as a DaemonSet in your cluster. Each daemon pod monitors node updates and, for each update, checks the 
node conditions. If a `VMEventScheduled` condition is present, it queries the [Instance Metadata Service](https://learn.microsoft.com/en-us/azure/virtual-machines/instance-metadata-service?tabs=linux) for maintenance
information.

If the maintenance event is deemed impactful, it will cordon the node and begin draining pods to other nodes in the cluster.
During the drain flow, a label is added to the node (`mechanic.cordoned`) indicating that it was cordoned by mechanic. If the daemon pod is restarted,
it will check for this label and use it as an input on whether to uncordon the node if the `VMEventScheduled` condition is
no longer present.
