import React, { useState, useEffect } from 'react';
import {
  HardDrive,
  Layers,
  Key,
  Share2,
  Activity,
  Plus,
  Trash2,
  RefreshCw,
  CheckCircle2,
  ShieldCheck,
  Copy,
  Check,
  Server,
  Database,
  ArrowRight,
  BookOpen,
  Send
} from 'lucide-react';

interface StatusData {
  status: string;
  version: string;
  uptime: string;
  total_buckets: number;
  total_objects: number;
  active_uploads: number;
  total_requests: number;
  disk: {
    total_bytes: number;
    used_bytes: number;
    free_bytes: number;
  };
}

interface Bucket {
  id: string;
  name: string;
  versioning_status: string;
  created_at: string;
}

interface S3Object {
  id: string;
  object_key: string;
  size: number;
  etag: string;
  content_type: string;
  created_at: string;
}

interface User {
  id: string;
  username: string;
  created_at: string;
}

interface AccessKey {
  access_key_id: string;
  user_id: string;
  status: string;
  created_at: string;
}

interface Node {
  id: string;
  address: string;
  status: string;
  last_heartbeat: string;
}

interface Conflict {
  id: number;
  bucket_name: string;
  object_key: string;
  resolution: string;
  detected_at: string;
}

export function App() {
  const [activeTab, setActiveTab] = useState<'overview' | 'buckets' | 'iam' | 'replication' | 'jobs' | 'api'>('overview');
  const [status, setStatus] = useState<StatusData | null>(null);
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [selectedBucket, setSelectedBucket] = useState<string | null>(null);
  const [bucketObjects, setBucketObjects] = useState<S3Object[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [accessKeys, setAccessKeys] = useState<AccessKey[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [conflicts, setConflicts] = useState<Conflict[]>([]);

  // Modals
  const [showCreateBucket, setShowCreateBucket] = useState(false);
  const [newBucketName, setNewBucketName] = useState('');
  const [newBucketVersioning, setNewBucketVersioning] = useState(false);

  const [showCreateUser, setShowCreateUser] = useState(false);
  const [newUsername, setNewUsername] = useState('');

  const [showCreateKey, setShowCreateKey] = useState(false);
  const [keyUsername, setKeyUsername] = useState('');
  const [generatedKey, setGeneratedKey] = useState<{ id: string; secret: string } | null>(null);
  const [copied, setCopied] = useState(false);

  // Job reports
  const [lastGC, setLastGC] = useState<any>(null);
  const [lastScan, setLastScan] = useState<any>(null);
  const [isRunningGC, setIsRunningGC] = useState(false);
  const [isRunningScan, setIsRunningScan] = useState(false);

  // Admin Token State
  const [adminToken, setAdminToken] = useState<string>(() => {
    return localStorage.getItem('storage_admin_token') || 'storageadminsecretkey123';
  });

  const apiFetch = (url: string, options: RequestInit = {}) => {
    const headers = new Headers(options.headers || {});
    if (adminToken && !headers.has('X-Admin-Token')) {
      headers.set('X-Admin-Token', adminToken);
    }
    return fetch(url, { ...options, headers });
  };

  // API Documentation State
  const [apiFilter, setApiFilter] = useState<'all' | 's3' | 'admin' | 'sdk' | 'tester'>('all');
  const [activeSdk, setActiveSdk] = useState<'aws-cli' | 'python' | 'go' | 'node' | 'curl'>('aws-cli');
  const [testEndpoint, setTestEndpoint] = useState('/api/v1/status');
  const [testResult, setTestResult] = useState<string | null>(null);
  const [testLoading, setTestLoading] = useState(false);
  const [copiedSdk, setCopiedSdk] = useState(false);

  const fetchAll = async () => {
    try {
      const [statusRes, bucketsRes, usersRes, keysRes, nodesRes, confRes, jobsRes] = await Promise.all([
        apiFetch('/api/v1/status').then(r => r.json()).catch(() => null),
        apiFetch('/api/v1/buckets').then(r => r.json()).catch(() => []),
        apiFetch('/api/v1/users').then(r => r.json()).catch(() => []),
        apiFetch('/api/v1/access-keys').then(r => r.json()).catch(() => []),
        apiFetch('/api/v1/nodes').then(r => r.json()).catch(() => []),
        apiFetch('/api/v1/replication/conflicts').then(r => r.json()).catch(() => []),
        apiFetch('/api/v1/jobs/status').then(r => r.json()).catch(() => ({}))
      ]);

      if (statusRes) setStatus(statusRes);
      if (Array.isArray(bucketsRes)) setBuckets(bucketsRes);
      if (Array.isArray(usersRes)) setUsers(usersRes);
      if (Array.isArray(keysRes)) setAccessKeys(keysRes);
      if (Array.isArray(nodesRes)) setNodes(nodesRes);
      if (Array.isArray(confRes)) setConflicts(confRes);
      if (jobsRes) {
        setLastGC(jobsRes.last_gc);
        setLastScan(jobsRes.last_integrity);
      }
    } catch (e) {
      console.error("Failed fetching dashboard data", e);
    }
  };

  useEffect(() => {
    fetchAll();
    const interval = setInterval(fetchAll, 5000);
    return () => clearInterval(interval);
  }, []);

  const fetchObjects = async (bucketName: string) => {
    try {
      const res = await apiFetch(`/api/v1/buckets/${bucketName}/objects`);
      const data = await res.json();
      setBucketObjects(Array.isArray(data) ? data : []);
      setSelectedBucket(bucketName);
    } catch (e) {
      console.error(e);
    }
  };

  const handleCreateBucket = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newBucketName.trim()) return;
    try {
      const res = await apiFetch('/api/v1/buckets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: newBucketName.trim(),
          versioning: newBucketVersioning ? 'ENABLED' : 'DISABLED'
        })
      });
      if (res.ok) {
        setShowCreateBucket(false);
        setNewBucketName('');
        fetchAll();
      } else {
        const err = await res.json();
        alert(err.error || 'Failed to create bucket');
      }
    } catch (err: any) {
      alert(err.message);
    }
  };

  const handleDeleteBucket = async (name: string) => {
    if (!confirm(`Are you sure you want to delete bucket '${name}'?`)) return;
    try {
      const res = await apiFetch(`/api/v1/buckets/${name}`, { method: 'DELETE' });
      if (res.ok) {
        if (selectedBucket === name) setSelectedBucket(null);
        fetchAll();
      } else {
        const err = await res.json();
        alert(err.error || 'Failed to delete bucket');
      }
    } catch (err: any) {
      alert(err.message);
    }
  };

  const handleCreateUser = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newUsername.trim()) return;
    try {
      const res = await apiFetch('/api/v1/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: newUsername.trim() })
      });
      if (res.ok) {
        setShowCreateUser(false);
        setNewUsername('');
        fetchAll();
      }
    } catch (err: any) {
      alert(err.message);
    }
  };

  const handleCreateKey = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const res = await apiFetch('/api/v1/access-keys', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: keyUsername })
      });
      if (res.ok) {
        const data = await res.json();
        setGeneratedKey({ id: data.access_key_id, secret: data.secret_key });
        fetchAll();
      }
    } catch (err: any) {
      alert(err.message);
    }
  };

  const handleDeleteKey = async (id: string) => {
    if (!confirm(`Revoke access key '${id}'?`)) return;
    await apiFetch(`/api/v1/access-keys/${id}`, { method: 'DELETE' });
    fetchAll();
  };

  const triggerGC = async () => {
    setIsRunningGC(true);
    try {
      const res = await apiFetch('/api/v1/jobs/gc', { method: 'POST' });
      const data = await res.json();
      setLastGC(data);
    } finally {
      setIsRunningGC(false);
    }
  };

  const triggerScan = async () => {
    setIsRunningScan(true);
    try {
      const res = await apiFetch('/api/v1/jobs/integrity', { method: 'POST' });
      const data = await res.json();
      setLastScan(data);
    } finally {
      setIsRunningScan(false);
    }
  };

  const formatBytes = (bytes: number) => {
    if (!bytes || bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  const diskUsedPct = status?.disk?.total_bytes
    ? Math.round((status.disk.used_bytes / status.disk.total_bytes) * 100)
    : 0;

  return (
    <div className="app-container">
      {/* Sidebar Navigation */}
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-icon">
            <HardDrive size={20} />
          </div>
          <span>S3 Engine</span>
        </div>

        <nav>
          <ul className="nav-menu">
            <li>
              <button
                className={`nav-item ${activeTab === 'overview' ? 'active' : ''}`}
                onClick={() => setActiveTab('overview')}
              >
                <Activity size={18} />
                <span>Overview</span>
              </button>
            </li>
            <li>
              <button
                className={`nav-item ${activeTab === 'buckets' ? 'active' : ''}`}
                onClick={() => setActiveTab('buckets')}
              >
                <Layers size={18} />
                <span>Buckets & Data</span>
              </button>
            </li>
            <li>
              <button
                className={`nav-item ${activeTab === 'iam' ? 'active' : ''}`}
                onClick={() => setActiveTab('iam')}
              >
                <Key size={18} />
                <span>Access & IAM</span>
              </button>
            </li>
            <li>
              <button
                className={`nav-item ${activeTab === 'replication' ? 'active' : ''}`}
                onClick={() => setActiveTab('replication')}
              >
                <Share2 size={18} />
                <span>Replication</span>
              </button>
            </li>
            <li>
              <button
                className={`nav-item ${activeTab === 'jobs' ? 'active' : ''}`}
                onClick={() => setActiveTab('jobs')}
              >
                <ShieldCheck size={18} />
                <span>Maintenance</span>
              </button>
            </li>
            <li>
              <button
                className={`nav-item ${activeTab === 'api' ? 'active' : ''}`}
                onClick={() => setActiveTab('api')}
              >
                <BookOpen size={18} />
                <span>API Details & Docs</span>
              </button>
            </li>
          </ul>
        </nav>

        <div style={{ marginTop: 'auto', padding: '16px', background: 'rgba(255,255,255,0.03)', borderRadius: '12px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '0.85rem' }}>
            <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: '#10b981' }}></span>
            <span style={{ fontWeight: 600 }}>Node 1: Online</span>
          </div>
          <p style={{ fontSize: '0.75rem', color: '#64748b', marginTop: '4px' }}>
            Port 9000 (S3) / 9001 (API)
          </p>
        </div>
      </aside>

      {/* Main Content Area */}
      <main className="main-content">
        {/* Top Header */}
        <header className="top-header">
          <div>
            <h1 className="page-title">
              {activeTab === 'overview' && 'Cluster Health & Metrics'}
              {activeTab === 'buckets' && 'Bucket & Object Explorer'}
              {activeTab === 'iam' && 'Users & Access Credentials'}
              {activeTab === 'replication' && 'Replication & Multi-Node Cluster'}
              {activeTab === 'jobs' && 'Storage Maintenance & Integrity'}
              {activeTab === 'api' && 'API Details & Documentation'}
            </h1>
            <p className="page-subtitle">
              Self-managed, high-throughput S3-compatible object storage
            </p>
          </div>

          <div style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
            <button
              className="btn btn-secondary"
              onClick={() => {
                const newToken = prompt("Enter Admin Token for REST API:", adminToken);
                if (newToken !== null) {
                  setAdminToken(newToken.trim());
                  localStorage.setItem('storage_admin_token', newToken.trim());
                  fetchAll();
                }
              }}
              title="Configure Admin API Token"
            >
              <Key size={16} />
              <span>Token</span>
            </button>
            <button className="btn btn-secondary" onClick={fetchAll}>
              <RefreshCw size={16} />
              <span>Refresh</span>
            </button>
            {activeTab === 'buckets' && (
              <button className="btn btn-primary" onClick={() => setShowCreateBucket(true)}>
                <Plus size={16} />
                <span>Create Bucket</span>
              </button>
            )}
            {activeTab === 'iam' && (
              <button className="btn btn-primary" onClick={() => setShowCreateKey(true)}>
                <Plus size={16} />
                <span>Create Access Key</span>
              </button>
            )}
          </div>
        </header>

        {/* TAB 1: OVERVIEW */}
        {activeTab === 'overview' && (
          <div>
            <div className="stats-grid">
              <div className="glass-card stat-card">
                <div className="stat-header">
                  <span>Storage Capacity</span>
                  <HardDrive size={18} color="#06b6d4" />
                </div>
                <div className="stat-value">{formatBytes(status?.disk?.used_bytes || 0)}</div>
                <div className="progress-bar">
                  <div className="progress-fill" style={{ width: `${diskUsedPct}%` }} />
                </div>
                <div className="stat-footer">
                  {diskUsedPct}% of {formatBytes(status?.disk?.total_bytes || 0)} capacity used
                </div>
              </div>

              <div className="glass-card stat-card">
                <div className="stat-header">
                  <span>Total Buckets</span>
                  <Layers size={18} color="#6366f1" />
                </div>
                <div className="stat-value">{status?.total_buckets ?? 0}</div>
                <div className="stat-footer">Across local storage volume</div>
              </div>

              <div className="glass-card stat-card">
                <div className="stat-header">
                  <span>Active Objects</span>
                  <Database size={18} color="#a855f7" />
                </div>
                <div className="stat-value">{status?.total_objects ?? 0}</div>
                <div className="stat-footer">Stored with hashed 2-level paths</div>
              </div>

              <div className="glass-card stat-card">
                <div className="stat-header">
                  <span>Total Requests</span>
                  <Activity size={18} color="#10b981" />
                </div>
                <div className="stat-value">{status?.total_requests ?? 0}</div>
                <div className="stat-footer">Active uploads: {status?.active_uploads ?? 0}</div>
              </div>
            </div>

            {/* Quick Actions & Jobs Summary */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '20px' }}>
              <div className="glass-card" style={{ padding: '24px' }}>
                <h3 style={{ fontSize: '1.1rem', fontWeight: 700, marginBottom: '8px' }}>
                  Garbage Collection (GC)
                </h3>
                <p style={{ color: '#94a3b8', fontSize: '0.88rem', marginBottom: '20px' }}>
                  Reclaim physical disk space by removing orphaned multipart chunks and old version files.
                </p>
                {lastGC && (
                  <div style={{ background: 'rgba(255,255,255,0.03)', padding: '14px', borderRadius: '8px', marginBottom: '16px', fontSize: '0.85rem' }}>
                    <div><strong>Last Run:</strong> {new Date(lastGC.start_time).toLocaleTimeString()} ({lastGC.duration})</div>
                    <div><strong>Freed:</strong> {formatBytes(lastGC.bytes_freed)} ({lastGC.orphan_parts_cleaned} parts cleaned)</div>
                  </div>
                )}
                <button className="btn btn-secondary" onClick={triggerGC} disabled={isRunningGC}>
                  <RefreshCw size={16} className={isRunningGC ? 'animate-spin' : ''} />
                  <span>{isRunningGC ? 'Running GC...' : 'Run GC Cycle Now'}</span>
                </button>
              </div>

              <div className="glass-card" style={{ padding: '24px' }}>
                <h3 style={{ fontSize: '1.1rem', fontWeight: 700, marginBottom: '8px' }}>
                  Integrity Scanner
                </h3>
                <p style={{ color: '#94a3b8', fontSize: '0.88rem', marginBottom: '20px' }}>
                  Verify on-disk byte integrity and CRC32C checksums for all objects against metadata.
                </p>
                {lastScan && (
                  <div style={{ background: 'rgba(255,255,255,0.03)', padding: '14px', borderRadius: '8px', marginBottom: '16px', fontSize: '0.85rem' }}>
                    <div><strong>Last Scan:</strong> {new Date(lastScan.start_time).toLocaleTimeString()} ({lastScan.duration})</div>
                    <div><strong>Results:</strong> {lastScan.passed} Passed / {lastScan.corrupted} Corrupted / {lastScan.missing} Missing</div>
                  </div>
                )}
                <button className="btn btn-secondary" onClick={triggerScan} disabled={isRunningScan}>
                  <ShieldCheck size={16} />
                  <span>{isRunningScan ? 'Scanning...' : 'Run Integrity Scan'}</span>
                </button>
              </div>
            </div>
          </div>
        )}

        {/* TAB 2: BUCKETS & OBJECTS */}
        {activeTab === 'buckets' && (
          <div>
            {!selectedBucket ? (
              <div className="glass-card table-container">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Bucket Name</th>
                      <th>Versioning</th>
                      <th>Created Date</th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {buckets.length === 0 ? (
                      <tr>
                        <td colSpan={4}>
                          <div className="empty-state">
                            <Layers size={32} />
                            <p>No buckets created yet. Click 'Create Bucket' to begin.</p>
                          </div>
                        </td>
                      </tr>
                    ) : (
                      buckets.map(b => (
                        <tr key={b.id}>
                          <td style={{ fontWeight: 600 }}>
                            <button
                              style={{ background: 'none', border: 'none', color: '#818cf8', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: '8px', fontSize: '0.95rem' }}
                              onClick={() => fetchObjects(b.name)}
                            >
                              <Layers size={16} />
                              <span>{b.name}</span>
                              <ArrowRight size={14} />
                            </button>
                          </td>
                          <td>
                            <span className={`badge ${b.versioning_status === 'ENABLED' ? 'badge-success' : 'badge-warning'}`}>
                              {b.versioning_status}
                            </span>
                          </td>
                          <td className="mono-text">{new Date(b.created_at).toLocaleString()}</td>
                          <td>
                            <button
                              className="btn btn-danger"
                              style={{ padding: '6px 12px', fontSize: '0.8rem' }}
                              onClick={() => handleDeleteBucket(b.name)}
                            >
                              <Trash2 size={14} />
                              <span>Delete</span>
                            </button>
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            ) : (
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '20px' }}>
                  <button className="btn btn-secondary" onClick={() => setSelectedBucket(null)}>
                    ← Back to Buckets
                  </button>
                  <h2 style={{ fontSize: '1.25rem', fontWeight: 700 }}>
                    Bucket: <span style={{ color: '#818cf8' }}>{selectedBucket}</span>
                  </h2>
                </div>

                <div className="glass-card table-container">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th>Object Key</th>
                        <th>Size</th>
                        <th>ETag</th>
                        <th>Content Type</th>
                        <th>Last Modified</th>
                      </tr>
                    </thead>
                    <tbody>
                      {bucketObjects.length === 0 ? (
                        <tr>
                          <td colSpan={5}>
                            <div className="empty-state">
                              <Database size={32} />
                              <p>Bucket is empty. Put objects via S3 API or AWS SDK.</p>
                            </div>
                          </td>
                        </tr>
                      ) : (
                        bucketObjects.map(obj => (
                          <tr key={obj.id}>
                            <td style={{ fontWeight: 600, fontFamily: 'var(--font-mono)' }}>{obj.object_key}</td>
                            <td>{formatBytes(obj.size)}</td>
                            <td className="mono-text">{obj.etag}</td>
                            <td><span className="badge badge-info">{obj.content_type}</span></td>
                            <td className="mono-text">{new Date(obj.created_at).toLocaleString()}</td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>
        )}

        {/* TAB 3: ACCESS & IAM */}
        {activeTab === 'iam' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '32px' }}>
            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
                <h3 style={{ fontSize: '1.2rem', fontWeight: 700 }}>Active Access Keys</h3>
                <button className="btn btn-primary" onClick={() => setShowCreateKey(true)}>
                  <Plus size={16} />
                  <span>Generate Key</span>
                </button>
              </div>

              <div className="glass-card table-container">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Access Key ID</th>
                      <th>User ID</th>
                      <th>Status</th>
                      <th>Created Date</th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {accessKeys.length === 0 ? (
                      <tr>
                        <td colSpan={5}>
                          <div className="empty-state">
                            <Key size={32} />
                            <p>No access keys. Create an access key for SigV4 clients.</p>
                          </div>
                        </td>
                      </tr>
                    ) : (
                      accessKeys.map(k => (
                        <tr key={k.access_key_id}>
                          <td className="mono-text" style={{ fontWeight: 600, color: '#f8fafc' }}>{k.access_key_id}</td>
                          <td className="mono-text">{k.user_id}</td>
                          <td>
                            <span className="badge badge-success">{k.status}</span>
                          </td>
                          <td className="mono-text">{new Date(k.created_at).toLocaleString()}</td>
                          <td>
                            <button
                              className="btn btn-danger"
                              style={{ padding: '6px 12px', fontSize: '0.8rem' }}
                              onClick={() => handleDeleteKey(k.access_key_id)}
                            >
                              <Trash2 size={14} />
                              <span>Revoke</span>
                            </button>
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </div>

            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
                <h3 style={{ fontSize: '1.2rem', fontWeight: 700 }}>Users</h3>
                <button className="btn btn-secondary" onClick={() => setShowCreateUser(true)}>
                  <Plus size={16} />
                  <span>Add User</span>
                </button>
              </div>

              <div className="glass-card table-container">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Username</th>
                      <th>User ID</th>
                      <th>Created Date</th>
                    </tr>
                  </thead>
                  <tbody>
                    {users.map(u => (
                      <tr key={u.id}>
                        <td style={{ fontWeight: 600 }}>{u.username}</td>
                        <td className="mono-text">{u.id}</td>
                        <td className="mono-text">{new Date(u.created_at).toLocaleString()}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        )}

        {/* TAB 4: REPLICATION */}
        {activeTab === 'replication' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '32px' }}>
            <div>
              <h3 style={{ fontSize: '1.2rem', fontWeight: 700, marginBottom: '16px' }}>Storage Cluster Nodes</h3>
              <div className="glass-card table-container">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Node ID</th>
                      <th>Address</th>
                      <th>Status</th>
                      <th>Last Heartbeat</th>
                    </tr>
                  </thead>
                  <tbody>
                    {nodes.length === 0 ? (
                      <tr>
                        <td colSpan={4}>
                          <div className="empty-state">
                            <Server size={32} />
                            <p>Single node mode active. Register peer nodes for automatic replication.</p>
                          </div>
                        </td>
                      </tr>
                    ) : (
                      nodes.map(n => (
                        <tr key={n.id}>
                          <td style={{ fontWeight: 600 }}>{n.id}</td>
                          <td className="mono-text">{n.address}</td>
                          <td><span className="badge badge-success">{n.status}</span></td>
                          <td className="mono-text">{new Date(n.last_heartbeat).toLocaleString()}</td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </div>

            <div>
              <h3 style={{ fontSize: '1.2rem', fontWeight: 700, marginBottom: '16px' }}>
                Replication Conflict Audit Logs
              </h3>
              <div className="glass-card table-container">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Bucket</th>
                      <th>Object Key</th>
                      <th>Resolution (LWW)</th>
                      <th>Detected At</th>
                    </tr>
                  </thead>
                  <tbody>
                    {conflicts.length === 0 ? (
                      <tr>
                        <td colSpan={4}>
                          <div className="empty-state">
                            <CheckCircle2 size={32} color="#10b981" />
                            <p>No conflict events. Cluster metadata is fully aligned.</p>
                          </div>
                        </td>
                      </tr>
                    ) : (
                      conflicts.map(c => (
                        <tr key={c.id}>
                          <td>{c.bucket_name}</td>
                          <td className="mono-text">{c.object_key}</td>
                          <td>
                            <span className="badge badge-warning">{c.resolution}</span>
                          </td>
                          <td className="mono-text">{new Date(c.detected_at).toLocaleString()}</td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        )}

        {/* TAB 5: MAINTENANCE */}
        {activeTab === 'jobs' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '32px' }}>
            {/* Maintenance Job Triggers */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>
              <div className="glass-card" style={{ padding: '28px' }}>
                <h3 style={{ fontSize: '1.2rem', fontWeight: 700, marginBottom: '12px' }}>Garbage Collection</h3>
                <p style={{ color: '#94a3b8', fontSize: '0.9rem', marginBottom: '24px' }}>
                  Purges orphaned multipart uploads and old version fragments from physical disk.
                </p>
                {lastGC && (
                  <div style={{ background: 'rgba(255,255,255,0.03)', padding: '16px', borderRadius: '10px', marginBottom: '20px' }}>
                    <div style={{ marginBottom: '6px' }}><strong>Status:</strong> Completed</div>
                    <div style={{ marginBottom: '6px' }}><strong>Orphan Parts Cleaned:</strong> {lastGC.orphan_parts_cleaned}</div>
                    <div style={{ marginBottom: '6px' }}><strong>Orphan Files Cleaned:</strong> {lastGC.orphan_files_cleaned}</div>
                    <div><strong>Total Reclaimed:</strong> {formatBytes(lastGC.bytes_freed)}</div>
                  </div>
                )}
                <button className="btn btn-primary" onClick={triggerGC} disabled={isRunningGC}>
                  <RefreshCw size={16} className={isRunningGC ? 'animate-spin' : ''} />
                  <span>{isRunningGC ? 'Executing GC...' : 'Trigger GC Cycle'}</span>
                </button>
              </div>

              <div className="glass-card" style={{ padding: '28px' }}>
                <h3 style={{ fontSize: '1.2rem', fontWeight: 700, marginBottom: '12px' }}>Data Integrity Scanner</h3>
                <p style={{ color: '#94a3b8', fontSize: '0.9rem', marginBottom: '24px' }}>
                  Computes on-disk CRC32C and SHA-256 hashes to detect silent bit rot or disk tampering.
                </p>
                {lastScan && (
                  <div style={{ background: 'rgba(255,255,255,0.03)', padding: '16px', borderRadius: '10px', marginBottom: '20px' }}>
                    <div style={{ marginBottom: '6px' }}><strong>Total Checked:</strong> {lastScan.total_checked}</div>
                    <div style={{ marginBottom: '6px', color: '#34d399' }}><strong>Passed:</strong> {lastScan.passed}</div>
                    <div style={{ marginBottom: '6px', color: '#fbbf24' }}><strong>Missing:</strong> {lastScan.missing}</div>
                    <div style={{ color: lastScan.corrupted > 0 ? '#f87171' : '#94a3b8' }}>
                      <strong>Corrupted:</strong> {lastScan.corrupted}
                    </div>
                  </div>
                )}
                <button className="btn btn-primary" onClick={triggerScan} disabled={isRunningScan}>
                  <ShieldCheck size={16} />
                  <span>{isRunningScan ? 'Scanning...' : 'Trigger Integrity Scan'}</span>
                </button>
              </div>
            </div>
          </div>
        )}

        {/* TAB 6: API DETAILS & DOCUMENTATION */}
        {activeTab === 'api' && (
          <div className="glass-card" style={{ padding: '32px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '24px' }}>
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                    <BookOpen size={24} color="#818cf8" />
                    <h2 style={{ fontSize: '1.4rem', fontWeight: 800 }}>API Details & Documentation</h2>
                  </div>
                  <p style={{ color: '#94a3b8', fontSize: '0.92rem', marginTop: '6px' }}>
                    Reference specifications and integration guides for S3 Storage (port 9000) and Management REST API (port 9001).
                  </p>
                </div>

                <div style={{ display: 'flex', gap: '8px' }}>
                  <span className="badge badge-info" style={{ padding: '6px 12px' }}>S3 Port: 9000</span>
                  <span className="badge badge-success" style={{ padding: '6px 12px' }}>Admin Port: 9001</span>
                </div>
              </div>

              {/* API Sub Navigation Tabs */}
              <div className="api-subnav" style={{ marginBottom: '28px' }}>
                <button
                  className={`api-subnav-btn ${apiFilter === 'all' ? 'active' : ''}`}
                  onClick={() => setApiFilter('all')}
                >
                  All Endpoints
                </button>
                <button
                  className={`api-subnav-btn ${apiFilter === 's3' ? 'active' : ''}`}
                  onClick={() => setApiFilter('s3')}
                >
                  S3 API (Port 9000)
                </button>
                <button
                  className={`api-subnav-btn ${apiFilter === 'admin' ? 'active' : ''}`}
                  onClick={() => setApiFilter('admin')}
                >
                  Management REST API (Port 9001)
                </button>
                <button
                  className={`api-subnav-btn ${apiFilter === 'sdk' ? 'active' : ''}`}
                  onClick={() => setApiFilter('sdk')}
                >
                  SDK Code Examples
                </button>
                <button
                  className={`api-subnav-btn ${apiFilter === 'tester' ? 'active' : ''}`}
                  onClick={() => setApiFilter('tester')}
                >
                  Live API Console
                </button>
              </div>

              {/* SUBVIEW: SDK EXAMPLES */}
              {apiFilter === 'sdk' && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
                  <div style={{ display: 'flex', gap: '10px', alignItems: 'center' }}>
                    <span style={{ fontSize: '0.88rem', fontWeight: 600, color: '#94a3b8' }}>Client Language:</span>
                    {(['aws-cli', 'python', 'node', 'go', 'curl'] as const).map(lang => (
                      <button
                        key={lang}
                        className={`btn ${activeSdk === lang ? 'btn-primary' : 'btn-secondary'}`}
                        style={{ padding: '6px 14px', fontSize: '0.82rem' }}
                        onClick={() => setActiveSdk(lang)}
                      >
                        {lang === 'aws-cli' && 'AWS CLI'}
                        {lang === 'python' && 'Python (Boto3)'}
                        {lang === 'node' && 'Node.js (AWS SDK v3)'}
                        {lang === 'go' && 'Go (aws-sdk-go-v2)'}
                        {lang === 'curl' && 'cURL'}
                      </button>
                    ))}
                  </div>

                  <div className="code-box">
                    <button
                      className="copy-btn"
                      onClick={() => {
                        const code = activeSdk === 'aws-cli' ? `# Configure AWS CLI environment
export AWS_ACCESS_KEY_ID="storageadmin"
export AWS_SECRET_ACCESS_KEY="storageadminsecretkey123"
export AWS_DEFAULT_REGION="us-east-1"

# 1. Create a Bucket
aws --endpoint-url http://localhost:9000 s3 mb s3://my-bucket

# 2. Upload a File
aws --endpoint-url http://localhost:9000 s3 cp ./file.txt s3://my-bucket/file.txt

# 3. List Objects
aws --endpoint-url http://localhost:9000 s3 ls s3://my-bucket/

# 4. Download Object
aws --endpoint-url http://localhost:9000 s3 cp s3://my-bucket/file.txt ./downloaded.txt`
                        : activeSdk === 'python' ? `import boto3

s3 = boto3.client(
    's3',
    endpoint_url='http://localhost:9000',
    aws_access_key_id='storageadmin',
    aws_secret_access_key='storageadminsecretkey123',
    region_name='us-east-1'
)

# Create Bucket
s3.create_bucket(Bucket='my-bucket')

# Upload Object
s3.put_object(
    Bucket='my-bucket',
    Key='hello.txt',
    Body=b'Hello from Python Boto3!',
    ContentType='text/plain'
)

# Download Object
response = s3.get_object(Bucket='my-bucket', Key='hello.txt')
print("Content:", response['Body'].read().decode('utf-8'))`
                        : activeSdk === 'node' ? `import { S3Client, PutObjectCommand, GetObjectCommand } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "http://localhost:9000",
  region: "us-east-1",
  credentials: {
    accessKeyId: "storageadmin",
    secretAccessKey: "storageadminsecretkey123",
  },
  forcePathStyle: true,
});

// Upload
await s3.send(new PutObjectCommand({
  Bucket: "my-bucket",
  Key: "sample.json",
  Body: JSON.stringify({ message: "Hello Node.js" }),
  ContentType: "application/json",
}));`
                        : activeSdk === 'go' ? `package main

import (
	"context"
	"fmt"
	"strings"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	cfg, _ := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("storageadmin", "storageadminsecretkey123", "")),
	)

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://localhost:9000")
		o.UsePathStyle = true
	})

	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String("my-bucket"),
		Key:    aws.String("hello.txt"),
		Body:   strings.NewReader("Hello from Go!"),
	})
	fmt.Println("Success:", err == nil)
}`
                        : `# 1. Health Check
curl -i http://localhost:9001/health

# 2. Prometheus Metrics
curl http://localhost:9001/metrics

# 3. Create bucket via Admin API
curl -X POST http://localhost:9001/api/v1/buckets \\
  -H "Content-Type: application/json" \\
  -d '{"name": "test-bucket", "versioning": "ENABLED"}'

# 4. Upload object via S3 API
curl -X PUT http://localhost:9000/test-bucket/file.txt \\
  --data-binary "Hello S3 Storage Engine" \\
  -H "Content-Type: text/plain"`;

                        navigator.clipboard.writeText(code);
                        setCopiedSdk(true);
                        setTimeout(() => setCopiedSdk(false), 2000);
                      }}
                    >
                      {copiedSdk ? <Check size={14} /> : <Copy size={14} />}
                      <span>{copiedSdk ? 'Copied' : 'Copy'}</span>
                    </button>

                    <pre style={{ margin: 0, paddingRight: '60px' }}>
                      {activeSdk === 'aws-cli' && `# Configure AWS CLI environment
export AWS_ACCESS_KEY_ID="storageadmin"
export AWS_SECRET_ACCESS_KEY="storageadminsecretkey123"
export AWS_DEFAULT_REGION="us-east-1"

# 1. Create a Bucket
aws --endpoint-url http://localhost:9000 s3 mb s3://my-bucket

# 2. Upload a File
aws --endpoint-url http://localhost:9000 s3 cp ./file.txt s3://my-bucket/file.txt

# 3. List Objects
aws --endpoint-url http://localhost:9000 s3 ls s3://my-bucket/

# 4. Download Object
aws --endpoint-url http://localhost:9000 s3 cp s3://my-bucket/file.txt ./downloaded.txt`}

                      {activeSdk === 'python' && `import boto3

s3 = boto3.client(
    's3',
    endpoint_url='http://localhost:9000',
    aws_access_key_id='storageadmin',
    aws_secret_access_key='storageadminsecretkey123',
    region_name='us-east-1'
)

# Create Bucket
s3.create_bucket(Bucket='my-bucket')

# Upload Object
s3.put_object(
    Bucket='my-bucket',
    Key='hello.txt',
    Body=b'Hello from Python Boto3!',
    ContentType='text/plain'
)

# Download Object
response = s3.get_object(Bucket='my-bucket', Key='hello.txt')
print("Content:", response['Body'].read().decode('utf-8'))`}

                      {activeSdk === 'node' && `import { S3Client, PutObjectCommand, GetObjectCommand } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "http://localhost:9000",
  region: "us-east-1",
  credentials: {
    accessKeyId: "storageadmin",
    secretAccessKey: "storageadminsecretkey123",
  },
  forcePathStyle: true,
});

// Upload
await s3.send(new PutObjectCommand({
  Bucket: "my-bucket",
  Key: "sample.json",
  Body: JSON.stringify({ message: "Hello Node.js" }),
  ContentType: "application/json",
}));`}

                      {activeSdk === 'go' && `package main

import (
	"context"
	"fmt"
	"strings"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	cfg, _ := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("storageadmin", "storageadminsecretkey123", "")),
	)

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://localhost:9000")
		o.UsePathStyle = true
	})

	_, err := client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String("my-bucket"),
		Key:    aws.String("hello.txt"),
		Body:   strings.NewReader("Hello from Go!"),
	})
	fmt.Println("Success:", err == nil)
}`}

                      {activeSdk === 'curl' && `# 1. Health Check
curl -i http://localhost:9001/health

# 2. Prometheus Metrics
curl http://localhost:9001/metrics

# 3. Create bucket via Admin API
curl -X POST http://localhost:9001/api/v1/buckets \\
  -H "Content-Type: application/json" \\
  -d '{"name": "test-bucket", "versioning": "ENABLED"}'

# 4. Upload object via S3 API
curl -X PUT http://localhost:9000/test-bucket/file.txt \\
  --data-binary "Hello S3 Storage Engine" \\
  -H "Content-Type: text/plain"`}
                    </pre>
                  </div>
                </div>
              )}

              {/* SUBVIEW: LIVE API TESTER */}
              {apiFilter === 'tester' && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
                  <p style={{ color: '#94a3b8', fontSize: '0.9rem' }}>
                    Send direct requests to the live server endpoints right from your browser console:
                  </p>

                  <div style={{ display: 'flex', gap: '10px', flexWrap: 'wrap' }}>
                    {[
                      { name: 'Server Status', path: '/api/v1/status' },
                      { name: 'Storage Metrics', path: '/api/v1/metrics' },
                      { name: 'List Buckets', path: '/api/v1/buckets' },
                      { name: 'List Users', path: '/api/v1/users' },
                      { name: 'Health Check', path: '/health' },
                      { name: 'Prometheus Metrics', path: '/metrics' }
                    ].map(test => (
                      <button
                        key={test.path}
                        className={`btn ${testEndpoint === test.path ? 'btn-primary' : 'btn-secondary'}`}
                        style={{ padding: '8px 14px', fontSize: '0.82rem' }}
                        onClick={async () => {
                          setTestEndpoint(test.path);
                          setTestLoading(true);
                          setTestResult(null);
                          try {
                            const res = await apiFetch(test.path);
                            const ct = res.headers.get('content-type') || '';
                            if (ct.includes('application/json')) {
                              const json = await res.json();
                              setTestResult(JSON.stringify(json, null, 2));
                            } else {
                              const text = await res.text();
                              setTestResult(text);
                            }
                          } catch (err: any) {
                            setTestResult('Error: ' + err.message);
                          } finally {
                            setTestLoading(false);
                          }
                        }}
                      >
                        <Send size={14} />
                        <span>{test.name}</span>
                      </button>
                    ))}
                  </div>

                  <div style={{ display: 'flex', gap: '10px', alignItems: 'center' }}>
                    <input
                      type="text"
                      className="form-input"
                      style={{ flex: 1, fontFamily: 'var(--font-mono)', fontSize: '0.88rem' }}
                      value={testEndpoint}
                      onChange={e => setTestEndpoint(e.target.value)}
                    />
                    <button
                      className="btn btn-primary"
                      disabled={testLoading}
                      onClick={async () => {
                        setTestLoading(true);
                        setTestResult(null);
                        try {
                          const res = await apiFetch(testEndpoint);
                          const ct = res.headers.get('content-type') || '';
                          if (ct.includes('application/json')) {
                            const json = await res.json();
                            setTestResult(JSON.stringify(json, null, 2));
                          } else {
                            const text = await res.text();
                            setTestResult(text);
                          }
                        } catch (err: any) {
                          setTestResult('Error: ' + err.message);
                        } finally {
                          setTestLoading(false);
                        }
                      }}
                    >
                      <Send size={16} />
                      <span>{testLoading ? 'Requesting...' : 'Send Request'}</span>
                    </button>
                  </div>

                  <div className="code-box" style={{ minHeight: '180px', maxHeight: '420px' }}>
                    {testLoading ? (
                      <div style={{ display: 'flex', alignItems: 'center', gap: '10px', color: '#94a3b8' }}>
                        <RefreshCw size={16} className="animate-spin" />
                        <span>Sending request to {testEndpoint}...</span>
                      </div>
                    ) : testResult ? (
                      <pre style={{ margin: 0 }}>{testResult}</pre>
                    ) : (
                      <span style={{ color: '#64748b' }}>Click an endpoint above or press 'Send Request' to view output.</span>
                    )}
                  </div>
                </div>
              )}

              {/* SUBVIEW: ENDPOINTS REFERENCE LIST */}
              {(apiFilter === 'all' || apiFilter === 's3' || apiFilter === 'admin') && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
                  {/* S3 Section Header */}
                  {(apiFilter === 'all' || apiFilter === 's3') && (
                    <div style={{ borderBottom: '1px solid var(--border-subtle)', paddingBottom: '8px', marginTop: '8px' }}>
                      <h3 style={{ fontSize: '1.1rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '8px' }}>
                        <HardDrive size={18} color="#06b6d4" />
                        <span>S3-Compatible Object & Bucket API (Port 9000)</span>
                      </h3>
                      <p style={{ color: '#94a3b8', fontSize: '0.84rem' }}>
                        Fully compatible with AWS SDKs, Boto3, AWS CLI, and SigV4 authentication.
                      </p>
                    </div>
                  )}

                  {(apiFilter === 'all' || apiFilter === 's3') && [
                    { method: 'GET', path: '/', title: 'List All Buckets', desc: 'Returns S3 XML list of all buckets owned by the user.' },
                    { method: 'PUT', path: '/{bucket}', title: 'Create Bucket', desc: 'Creates a new storage bucket.' },
                    { method: 'HEAD', path: '/{bucket}', title: 'Head Bucket', desc: 'Checks bucket existence (returns HTTP 200 or 404).' },
                    { method: 'DELETE', path: '/{bucket}', title: 'Delete Bucket', desc: 'Deletes an empty bucket.' },
                    { method: 'GET', path: '/{bucket}?list-type=2', title: 'ListObjectsV2', desc: 'Lists bucket objects with keyset pagination, prefix, and delimiter.' },
                    { method: 'PUT', path: '/{bucket}/{key}', title: 'Put Object', desc: 'Streams object data to disk, computing CRC32C, SHA-256 and MD5 ETag on the fly.' },
                    { method: 'GET', path: '/{bucket}/{key}', title: 'Get Object', desc: 'Streams object data with transparent decryption and HTTP Range (bytes=start-end) support.' },
                    { method: 'HEAD', path: '/{bucket}/{key}', title: 'Head Object', desc: 'Returns object headers (ETag, Content-Length, Content-Type, CRC32C) without body.' },
                    { method: 'DELETE', path: '/{bucket}/{key}', title: 'Delete Object', desc: 'Creates delete marker if versioned, or hard deletes object data.' },
                    { method: 'POST', path: '/{bucket}/{key}?uploads', title: 'Create Multipart Upload', desc: 'Initiates a multipart upload and returns Upload ID.' },
                    { method: 'PUT', path: '/{bucket}/{key}?uploadId={id}&partNumber={n}', title: 'Upload Part', desc: 'Uploads a specific part chunk for a multipart upload.' },
                    { method: 'POST', path: '/{bucket}/{key}?uploadId={id}', title: 'Complete Multipart Upload', desc: 'Assembles uploaded part files into the final completed object.' },
                    { method: 'DELETE', path: '/{bucket}/{key}?uploadId={id}', title: 'Abort Multipart Upload', desc: 'Cancels upload and purges temporary part chunks from disk.' }
                  ].map((ep, idx) => (
                    <div key={idx} className="glass-card api-endpoint-card">
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '14px' }}>
                          <span className={`method-badge method-${ep.method.toLowerCase()}`}>{ep.method}</span>
                          <span style={{ fontFamily: 'var(--font-mono)', fontWeight: 600, fontSize: '0.92rem' }}>
                            http://localhost:9000{ep.path}
                          </span>
                        </div>
                        <span style={{ fontSize: '0.85rem', fontWeight: 600, color: '#818cf8' }}>{ep.title}</span>
                      </div>
                      <p style={{ color: '#94a3b8', fontSize: '0.85rem' }}>{ep.desc}</p>
                    </div>
                  ))}

                  {/* Admin Section Header */}
                  {(apiFilter === 'all' || apiFilter === 'admin') && (
                    <div style={{ borderBottom: '1px solid var(--border-subtle)', paddingBottom: '8px', marginTop: '24px' }}>
                      <h3 style={{ fontSize: '1.1rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '8px' }}>
                        <Activity size={18} color="#a855f7" />
                        <span>Management REST API & Observability (Port 9001)</span>
                      </h3>
                      <p style={{ color: '#94a3b8', fontSize: '0.84rem' }}>
                        High-speed JSON endpoints for dashboard operations, CLI tooling, and Prometheus metrics.
                      </p>
                    </div>
                  )}

                  {(apiFilter === 'all' || apiFilter === 'admin') && [
                    { method: 'GET', path: '/api/v1/status', title: 'System Status & Disk Usage', desc: 'Returns cluster health, disk capacity (total/used/free), and uptime.' },
                    { method: 'GET', path: '/api/v1/metrics', title: 'Aggregated Metrics', desc: 'Returns object counts, bucket counts, and traffic statistics.' },
                    { method: 'GET', path: '/api/v1/buckets', title: 'List Buckets (JSON)', desc: 'Returns array of all buckets with versioning configurations.' },
                    { method: 'POST', path: '/api/v1/buckets', title: 'Create Bucket (JSON)', desc: 'Creates a bucket with payload {"name": "...", "versioning": "ENABLED"}.' },
                    { method: 'DELETE', path: '/api/v1/buckets/{name}', title: 'Delete Bucket (JSON)', desc: 'Deletes bucket by name.' },
                    { method: 'GET', path: '/api/v1/buckets/{name}/objects', title: 'Inspect Bucket Objects', desc: 'Lists objects inside a bucket in JSON format.' },
                    { method: 'GET', path: '/api/v1/users', title: 'List IAM Users', desc: 'Returns all registered IAM storage users.' },
                    { method: 'POST', path: '/api/v1/users', title: 'Create User', desc: 'Creates an IAM user with {"username": "..."}.' },
                    { method: 'GET', path: '/api/v1/access-keys', title: 'List Access Keys', desc: 'Returns active access keys for client authentication.' },
                    { method: 'POST', path: '/api/v1/access-keys', title: 'Create Access Key', desc: 'Generates Access Key ID and Secret Key with one-time secret revelation.' },
                    { method: 'DELETE', path: '/api/v1/access-keys/{id}', title: 'Revoke Access Key', desc: 'Revokes and deletes an access key.' },
                    { method: 'POST', path: '/api/v1/jobs/gc', title: 'Trigger Garbage Collection', desc: 'Executes an immediate GC cycle to clean orphaned parts and old files.' },
                    { method: 'POST', path: '/api/v1/jobs/integrity', title: 'Trigger Integrity Scan', desc: 'Initiates a bit-rot scan across all stored objects.' },
                    { method: 'GET', path: '/health', title: 'Health Probe', desc: 'Health check probe returning HTTP 200 (healthy) or 503 (unhealthy).' },
                    { method: 'GET', path: '/metrics', title: 'Prometheus Exporter', desc: 'Standard Prometheus metrics output for Grafana and alert managers.' }
                  ].map((ep, idx) => (
                    <div key={idx} className="glass-card api-endpoint-card">
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '14px' }}>
                          <span className={`method-badge method-${ep.method.toLowerCase()}`}>{ep.method}</span>
                          <span style={{ fontFamily: 'var(--font-mono)', fontWeight: 600, fontSize: '0.92rem' }}>
                            http://localhost:9001{ep.path}
                          </span>
                        </div>
                        <span style={{ fontSize: '0.85rem', fontWeight: 600, color: '#818cf8' }}>{ep.title}</span>
                      </div>
                      <p style={{ color: '#94a3b8', fontSize: '0.85rem' }}>{ep.desc}</p>
                    </div>
                  ))}
                </div>
              )}
            </div>
        )}
      </main>

      {/* MODAL: Create Bucket */}
      {showCreateBucket && (
        <div className="modal-overlay" onClick={() => setShowCreateBucket(false)}>
          <div className="modal-content" onClick={e => e.stopPropagation()}>
            <h3 style={{ fontSize: '1.3rem', fontWeight: 700, marginBottom: '16px' }}>Create New Bucket</h3>
            <form onSubmit={handleCreateBucket}>
              <div className="form-group">
                <label className="form-label">Bucket Name</label>
                <input
                  type="text"
                  className="form-input"
                  placeholder="e.g. photos-backup"
                  value={newBucketName}
                  onChange={e => setNewBucketName(e.target.value)}
                  required
                />
              </div>

              <div className="form-group" style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                <input
                  type="checkbox"
                  id="versioning"
                  checked={newBucketVersioning}
                  onChange={e => setNewBucketVersioning(e.target.checked)}
                />
                <label htmlFor="versioning" style={{ fontSize: '0.9rem', cursor: 'pointer' }}>
                  Enable Object Versioning
                </label>
              </div>

              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', marginTop: '24px' }}>
                <button type="button" className="btn btn-secondary" onClick={() => setShowCreateBucket(false)}>
                  Cancel
                </button>
                <button type="submit" className="btn btn-primary">
                  Create Bucket
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* MODAL: Create User */}
      {showCreateUser && (
        <div className="modal-overlay" onClick={() => setShowCreateUser(false)}>
          <div className="modal-content" onClick={e => e.stopPropagation()}>
            <h3 style={{ fontSize: '1.3rem', fontWeight: 700, marginBottom: '16px' }}>Create IAM User</h3>
            <form onSubmit={handleCreateUser}>
              <div className="form-group">
                <label className="form-label">Username</label>
                <input
                  type="text"
                  className="form-input"
                  placeholder="e.g. alice"
                  value={newUsername}
                  onChange={e => setNewUsername(e.target.value)}
                  required
                />
              </div>

              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', marginTop: '24px' }}>
                <button type="button" className="btn btn-secondary" onClick={() => setShowCreateUser(false)}>
                  Cancel
                </button>
                <button type="submit" className="btn btn-primary">
                  Create User
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* MODAL: Create Access Key */}
      {showCreateKey && (
        <div className="modal-overlay" onClick={() => { setShowCreateKey(false); setGeneratedKey(null); }}>
          <div className="modal-content" onClick={e => e.stopPropagation()}>
            {!generatedKey ? (
              <>
                <h3 style={{ fontSize: '1.3rem', fontWeight: 700, marginBottom: '16px' }}>Generate Access Key</h3>
                <form onSubmit={handleCreateKey}>
                  <div className="form-group">
                    <label className="form-label">Select User</label>
                    <select
                      className="form-input"
                      value={keyUsername}
                      onChange={e => setKeyUsername(e.target.value)}
                      required
                    >
                      <option value="">-- Choose User --</option>
                      {users.map(u => (
                        <option key={u.id} value={u.username}>{u.username}</option>
                      ))}
                    </select>
                  </div>

                  <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', marginTop: '24px' }}>
                    <button type="button" className="btn btn-secondary" onClick={() => setShowCreateKey(false)}>
                      Cancel
                    </button>
                    <button type="submit" className="btn btn-primary" disabled={!keyUsername}>
                      Generate Key
                    </button>
                  </div>
                </form>
              </>
            ) : (
              <>
                <div style={{ display: 'flex', alignItems: 'center', gap: '10px', color: '#10b981', marginBottom: '16px' }}>
                  <CheckCircle2 size={24} />
                  <h3 style={{ fontSize: '1.25rem', fontWeight: 700 }}>Access Key Created</h3>
                </div>
                <p style={{ color: '#94a3b8', fontSize: '0.85rem', marginBottom: '20px' }}>
                  Please save the Secret Key now. It will not be shown again.
                </p>

                <div className="form-group">
                  <label className="form-label">Access Key ID</label>
                  <input type="text" className="form-input" readOnly value={generatedKey.id} />
                </div>

                <div className="form-group">
                  <label className="form-label">Secret Key</label>
                  <input type="text" className="form-input" readOnly value={generatedKey.secret} />
                </div>

                <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', marginTop: '24px' }}>
                  <button
                    className="btn btn-secondary"
                    onClick={() => {
                      navigator.clipboard.writeText(`AccessKeyId: ${generatedKey.id}\nSecretAccessKey: ${generatedKey.secret}`);
                      setCopied(true);
                      setTimeout(() => setCopied(false), 2000);
                    }}
                  >
                    {copied ? <Check size={16} /> : <Copy size={16} />}
                    <span>{copied ? 'Copied!' : 'Copy Credentials'}</span>
                  </button>
                  <button className="btn btn-primary" onClick={() => { setShowCreateKey(false); setGeneratedKey(null); }}>
                    Done
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export default App;
