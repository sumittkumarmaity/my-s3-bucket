# Storage Engine Capacity Planning & Sizing Guide

## 1. Capacity Sizing Formulas

### 1.1 Usable Raw Disk Storage Calculation
$$S_{\text{raw}} = \frac{N_{\text{objects}} \times S_{\text{avg}} \times R_{\text{factor}}}{1 - T_{\text{reserve}}}$$

Where:
- $N_{\text{objects}}$: Total planned number of stored objects.
- $S_{\text{avg}}$: Average object size in bytes.
- $R_{\text{factor}}$: Replication redundancy multiplier ($R=1$ for single node, $R=3$ for 3-node cluster).
- $T_{\text{reserve}}$: Headroom reserve fraction (recommend minimum 15-20% free space buffer to satisfy write admission control).

### 1.2 Inode Consumption Calculation
In Linux filesystems (ext4/XFS), each stored object consumes:
1. One data file inode (`UUID.data`).
2. Sibling temporary inodes during active multipart / upload phases.

$$\text{Required Inodes} = N_{\text{objects}} \times 1.25 + 65,536 \text{ (Fanout Dirs)}$$

> [!IMPORTANT]
> When formatting ext4 filesystems for massive small-object storage, specify `-N <inode_count>` or use XFS with dynamic inode allocation:
> ```bash
> mkfs.xfs -m crc=1,finobt=1 /dev/sdb1
> ```

---

## 2. Hardware Dimensioning Profiles

| Workload Profile | Target Object Count | Storage Capacity | RAM Minimum | CPU Sizing | Recommended Storage Media |
|---|---|---|---|---|---|
| **Small Enterprise / Branch** | Up to 1,000,000 | 5 TB – 20 TB | 8 GB | 4 Cores | SATA / SAS Enterprise SSDs |
| **Mid-Market / Analytics** | 1,000,000 – 10,000,000 | 20 TB – 100 TB | 32 GB | 16 Cores | NVMe PCIe Gen4 SSDs |
| **Tier-1 Enterprise Core** | > 50,000,000 | 100 TB – 1 PB+ | 128 GB | 32+ Cores | Enterprise NVMe U.2 / U.3 in Hardware RAID10 |

---

## 3. Memory & RAM Allocation

The storage engine is designed with minimal static heap footprint. Memory is governed by:
- **Goroutine Stacks**: $\approx 4\text{ KB}$ per concurrent HTTP connection ($10,000\text{ connections} \approx 40\text{ MB}$).
- **Streaming Buffers**: $\approx 64\text{ KB}$ per in-flight AES-GCM encryption chunk ($500\text{ concurrent writes} \approx 32\text{ MB}$).
- **SQLite Page Cache**: Configurable (`PRAGMA cache_size = -64000`, allocating $\approx 64\text{ MB}$).
- **Filesystem Page Cache**: Remaining host RAM automatically accelerates Linux kernel buffer cache for read performance.
