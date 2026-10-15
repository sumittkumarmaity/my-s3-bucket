# Production Security Hardening & Compliance Guide

## 1. Operating System & Process Sandboxing (systemd)

Run the storage engine daemon as a non-privileged dedicated service user (`storage`) with strict Linux capabilities and system call filters.

Create `/etc/systemd/system/storage-server.service`:

```ini
[Unit]
Description=Enterprise S3-Compatible Storage Engine Daemon
After=network.target local-fs.target
Documentation=https://github.com/my-s3-bucket/storage-engine

[Service]
Type=simple
User=storage
Group=storage
WorkingDirectory=/opt/storage-engine
ExecStart=/opt/storage-engine/bin/storage-server --config /etc/storage-engine/config.yaml
Restart=always
RestartSec=5s

# Process Sandbox & Capability Hardening
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictRealtime=true
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true

# Filesystem Access Permissions
ReadWritePaths=/data/storage /data/metadata /var/log/storage-engine
ReadOnlyPaths=/etc/storage-engine

# Resource Limits
LimitNOFILE=1048576
LimitNPROC=65536
LimitMEMLOCK=infinity

[Install]
WantedBy=multi-user.target
```

---

## 2. Filesystem & Directory Permissions

Enforce least privilege on host filesystems:

```bash
# Create dedicated user and group
groupadd -r storage
useradd -r -g storage -d /var/lib/storage-engine -s /sbin/nologin storage

# Lock configuration directory and secrets
chown -R root:storage /etc/storage-engine
chmod 0750 /etc/storage-engine
chmod 0640 /etc/storage-engine/config.yaml

# Data storage paths
chown -R storage:storage /data/storage /data/metadata /var/log/storage-engine
chmod 0700 /data/storage /data/metadata
```

---

## 3. Cryptographic Key Rotation Protocol

### 3.1 Rotating Admin & Service Secrets
1. Generate high-entropy 256-bit credentials:
   ```bash
   openssl rand -hex 32
   ```
2. Update `admin_secret_key` or `admin_token` in `/etc/storage-engine/config.yaml` or inject via `STORAGE_ADMIN_SECRET_KEY`.
3. Reload the daemon with zero service disruption.

### 3.2 Master SSE Key Re-encryption
When rotating `cfg.Security.EncryptionKey`:
1. The engine supports multi-generation key wrapping.
2. Background re-encryption passes decrypt using old key $K_1$ and write ciphertext with new key $K_2$.

---

## 4. Network Firewall & Port Isolation

```bash
# S3 Client API (Allow intranet / authorized subnets)
ufw allow from 10.0.0.0/8 to any port 9000 proto tcp comment "S3 API"

# Admin & Replication Sync API (Restricted strictly to management subnets and peer nodes)
ufw allow from 10.0.10.0/24 to any port 9001 proto tcp comment "Storage Admin and Peer Replication"

# Default deny incoming
ufw default deny incoming
ufw enable
```
