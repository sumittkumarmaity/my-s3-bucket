# Distributed Replication Topology & Conflict Resolution

## 1. Replication Architecture Overview

The storage engine implements an asynchronous, active-active multi-peer replication mesh designed for distributed high availability across enterprise sites, availability zones, or edge data centers.

```mermaid
graph LR
    subgraph "Site 1 (Primary / DC-East)"
        S1_S3["S3 API"] --> S1_Meta["Outbox Table"]
        S1_Worker["Replication Worker"] -->|Fetch Pending| S1_Meta
        S1_Disk["Storage: /data/storage"]
    end

    subgraph "Site 2 (Replica / DC-West)"
        S2_Receiver["Sync Handler (/internal/replication/sync)"]
        S2_Meta["Metadata Store"]
        S2_Disk["Storage: /data/storage"]
    end

    S1_Worker -->|Streaming HTTP POST + X-Replication Headers| S2_Receiver
    S2_Receiver -->|Atomic Write| S2_Disk
    S2_Receiver -->|Deterministic Conflict Resolution| S2_Meta
    S1_Worker -->|Update Status| S1_PeerTable["replication_peer_status"]
```

---

## 2. Multi-Peer Independent Delivery Tracking

In multi-node topologies (e.g. 3 or more nodes: Node A, Node B, Node C), replication tasks are tracked on a per-peer basis via the `replication_peer_status` table:
1. When an object mutation occurs on Node A, a job is inserted into `replication_outbox`.
2. The background replication worker queries registered peers from `storage_nodes`.
3. For each remote peer, the worker queries:
   ```sql
   SELECT status, retry_count FROM replication_peer_status 
   WHERE outbox_id = ? AND peer_id = ?;
   ```
4. If a peer has already received the payload (`status = 'COMPLETED'`), it is skipped.
5. If Peer B is offline or network fails, its status is recorded as `FAILED` with an incremented `retry_count`, while Peer C receives the replication successfully.
6. The outbox job transitions to `COMPLETED` only after all peers have confirmed successful delivery.

---

## 3. Conflict Resolution Algorithm

In active-active multi-master systems, concurrent writes to the same bucket and object key at different sites can produce conflict anomalies. The engine resolves conflicts using deterministic **Last-Write-Wins (LWW)** governed by sub-microsecond timestamps and deletion marker precedence.

```mermaid
flowchart TD
    Start["Remote Replication Event Received (Bucket, Key, Version, RemoteTS)"] --> CheckExists{"Does Local Object Exist?"}
    CheckExists -- No --> Accept["Store Object & Commit Metadata (ACCEPT)"]
    CheckExists -- Yes --> CompareTS{"Is RemoteTS > LocalTS?"}
    CompareTS -- Yes --> Overwrite["Overwrite Local Object & Version (REMOTE_KEPT)"]
    CompareTS -- No --> Reject["Reject Remote Object (LOCAL_KEPT)"]

    Overwrite --> LogConflict["Record Conflict in replication_conflicts Table"]
    Reject --> LogConflict
```

### 3.1 Conflict Resolution Rules
1. **Timestamp Precedence**:
   If an object exists locally with timestamp $T_{\text{local}}$ and an incoming replication update has $T_{\text{remote}}$:
   - If $T_{\text{remote}} > T_{\text{local}}$: Remote mutation wins. Local object is updated (`REMOTE_KEPT`).
   - If $T_{\text{remote}} < T_{\text{local}}$: Local mutation wins. Remote payload is dropped (`LOCAL_KEPT`).
2. **Versioned Objects**:
   In version-enabled buckets, both versions are preserved with their unique UUIDs, avoiding data loss entirely.
3. **Conflict Audit Trail**:
   All resolved conflicts are persisted in the `replication_conflicts` table and viewable via the Admin API (`GET /api/v1/replication/conflicts`) and Web Dashboard for operational auditing.
