# Enterprise High Availability (HA) & Clustering Guide

## 1. High Availability Architecture

For mission-critical enterprise environments, the storage engine is deployed across 3 or more independent physical nodes fronted by an enterprise load balancer (HAProxy, NGINX, or F5 BIG-IP).

```mermaid
graph TD
    Client["Enterprise Applications / S3 Clients"] --> VIP["Virtual IP (Keepalived / DNS Round Robin)"]
    VIP --> LB["Active/Passive HAProxy Load Balancers"]

    LB -->|S3 Traffic (Port 9000)| Node1["Storage Node 1 (DC-1)"]
    LB -->|S3 Traffic (Port 9000)| Node2["Storage Node 2 (DC-2)"]
    LB -->|S3 Traffic (Port 9000)| Node3["Storage Node 3 (DC-3)"]

    Node1 <==>|Active-Active Replication| Node2
    Node2 <==>|Active-Active Replication| Node3
    Node3 <==>|Active-Active Replication| Node1
```

---

## 2. Load Balancer Configuration (HAProxy Example)

Create `/etc/haproxy/haproxy.cfg`:

```haproxy
global
    log /dev/log local0
    maxconn 50000

defaults
    log     global
    mode    http
    option  httplog
    option  dontlognull
    retries 3
    timeout connect 5000ms
    timeout client  300000ms
    timeout server  300000ms

# S3 Object Storage Service
frontend s3_front
    bind *:443 ssl crt /etc/ssl/certs/s3-storage.pem
    default_backend s3_nodes

backend s3_nodes
    balance roundrobin
    option httpchk GET /health
    http-check expect status 200
    server node1 10.0.10.11:9000 check port 9001 inter 3000ms rise 2 fall 3
    server node2 10.0.10.12:9000 check port 9001 inter 3000ms rise 2 fall 3
    server node3 10.0.10.13:9000 check port 9001 inter 3000ms rise 2 fall 3

# Cluster Admin Service
frontend admin_front
    bind *:9001 ssl crt /etc/ssl/certs/s3-storage.pem
    default_backend admin_nodes

backend admin_nodes
    balance source
    server node1 10.0.10.11:9001 check inter 5000ms
    server node2 10.0.10.12:9001 check inter 5000ms
    server node3 10.0.10.13:9001 check inter 5000ms
```

---

## 3. Node Failure Domains & Recovery Scenarios

| Failure Event | Impact | Automatic Mitigation | Manual Action Required |
|---|---|---|---|
| **Single Node Crash (e.g. Node 1)** | Zero read/write downtime. | LB health check drops Node 1 in $< 6\text{s}$. Traffic shifts to Node 2 & 3. | Investigate hardware/OS logs on Node 1; restart service. |
| **Node 1 Restarts After Downtime** | Node 1 is behind by $N$ mutations. | Node 2 & 3 replication workers detect Node 1 online; flush pending outbox backlog. | Monitor `/metrics` until `replication_pending_jobs` reaches 0. |
| **Split-Brain Network Partition** | Isolated nodes continue servicing local writes. | Deterministic Last-Write-Wins (LWW) timestamp rule reconciles updates upon partition heal. | Review `/api/v1/replication/conflicts` to verify conflict resolutions. |
