# Mechanic

> Itelligently managing your AKS nodes when platform maintenance is required.

## Description

**mechanic** is a tool for AKS clusters that helps mitigate the impact from platform maintenance events. Its primary focus
is preventing application impacts from maintenance events that require node reboots or live migrations without moving pods
unnecessarily or causing application downtime.

It does this by monitoring node conditions and, when a maintenance event is indicated, querying the Instance Metadata Service
for maintenance event details. If the event is deemed impactful to the node, it will cordon and drain the node to ensure 
pods are rescheduled to other nodes before the maintenance event occurs.