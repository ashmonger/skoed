# Technical Specifications

This directory contains technical specifications that describe HOW functional requirements are implemented.

## Rules (from AGENTS.md)

- HTTP contracts MUST be specified in OpenAPI
- Async contracts MUST be specified in AsyncAPI
- Every technical artifact MUST include:
  - `x-tsid: TS-<TitleCamelCase>`
  - `x-fsid-links: [FS-...]`
- TSIDs MUST be unique and map to at least one FSID
- Technical specs MUST be user-validated before moving to tests
- Technical spec MAY need `<TitleCamelCase>.md` files for sequence diagrams, flowcharts, DMN, SLO

## Index

### Milestone 1
- `management-api.openapi.yaml` (TS-ManagementApi) — full REST contract for single-node management
- `dns-engine.md` (TS-DnsEngine) — query pipeline, forwarder, recursor, cache
- `config-schema.md` (TS-ConfigSchema) — YAML schema; M2 import/export only

### Milestone 2
- `management-api.openapi.yaml` (TS-ClusterApi, TS-QueryLogCluster) — extended with `/api/v1/cluster/*` endpoints
- `raft-fsm.md` (TS-RaftFsm) — FSM command set, snapshot, apply/restore semantics
- `cluster-store.md` (TS-ClusterStore) — on-disk layout: node.yaml, cluster.bbolt, querylog.bbolt, raft/
- `query-log-cluster.md` (TS-QueryLogCluster) — hourly aggregates via Raft + fan-out for raw entries
