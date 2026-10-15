# Linux Kernel, Filesystem & SQLite Performance Tuning Guide

## 1. Operating System Kernel Tuning (`/etc/sysctl.d/99-storage.conf`)

Apply high-throughput storage parameters for production nodes:

```ini
# Maximum open files and file handles
fs.file-max = 2097152

# Network socket backlog and buffer limits
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 16384
net.ipv4.tcp_max_syn_backlog = 16384

# TCP window size and memory buffers
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216

# Socket reuse and connection timeouts
net.ipv4.tcp_tw_reuse = 1
net.ipv4.tcp_fin_timeout = 15

# Virtual memory dirty ratio tuning (prevents disk write spikes)
vm.dirty_background_ratio = 5
vm.dirty_ratio = 10
vm.swappiness = 10
```

Apply with:
```bash
sysctl -p /etc/sysctl.d/99-storage.conf
```

---

## 2. Filesystem Mount Flags (`/etc/fstab`)

When mounting local NVMe or SSD storage for `/data`:

```fstab
# XFS (Recommended)
UUID=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx /data xfs noatime,nodiratime,logbufs=8,logbsize=256k,allocsize=64M 0 2

# ext4
UUID=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx /data ext4 noatime,nodiratime,data=writeback,barrier=0,nobh 0 2
```

- **`noatime,nodiratime`**: Eliminates unnecessary disk writes on read/stat operations.
- **`logbsize=256k`**: Increases XFS journal buffer for heavy concurrent write bursts.

---

## 3. SQLite Database Engine Tuning

The engine initializes SQLite with optimized flags in `internal/metadata/sqlite.go`:
- **`_pragma=busy_timeout(10000)`**: Waits up to 10 seconds during lock contention instead of failing immediately with `SQLITE_BUSY`.
- **`_pragma=journal_mode(WAL)`**: Write-Ahead Logging allows concurrent readers to operate simultaneously with write transactions.
- **`_pragma=foreign_keys(ON)`**: Ensures relational cascades and prevents orphaned foreign key records.
- **Connection Pooling**:
  - `MaxOpenConns(25)`: Keeps concurrent read pool active.
  - `MaxIdleConns(10)`: Reuses connections to eliminate open/close overhead.
  - `ConnMaxLifetime(1h)`: Periodic recycling avoids memory fragmentation.
