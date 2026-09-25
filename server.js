/**
 * Docker Control Panel - 后端服务（零依赖，纯 Node.js）
 * 架构参考 diancup（Go+Gin 单体思路），功能面对齐 diancup MVP：
 *   容器列表/启停/重启/删除 + 日志流 + 资源监控 + 镜像列表
 * Docker 连接：Windows 命名管道 或 Linux unix socket；连不上自动进入 Mock 模式（UI 预览用）
 */
'use strict';
const http = require('http');
const fs = require('fs');
const path = require('path');

const PORT = process.env.PANEL_PORT || 33335;
const PUBLIC_DIR = path.join(__dirname, 'public');

// ---------- Docker Engine API 客户端 ----------
const DOCKER_PIPE_WIN = '\\\\.\\pipe\\docker_engine';
const DOCKER_SOCK_LINUX = '/var/run/docker.sock';

const dockerPath = process.platform === 'win32' ? DOCKER_PIPE_WIN : DOCKER_SOCK_LINUX;
let dockerAvailable = null; // null=未探测

function dockerRequest(reqPath, method = 'GET', body = null, timeoutMs = 15000) {
  return new Promise((resolve, reject) => {
    const opts = { socketPath: dockerPath, path: '/v1.43' + reqPath, method, timeout: timeoutMs };
    let req;
    try {
      req = http.request(opts, (res) => {
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => resolve({ status: res.statusCode, buf: Buffer.concat(chunks) }));
      });
    } catch (e) { return reject(e); }
    req.on('timeout', () => { req.destroy(new Error('docker socket timeout')); });
    req.on('error', reject);
    if (body) req.write(body);
    req.end();
  });
}

async function detectDocker() {
  if (dockerAvailable !== null) return dockerAvailable;
  try {
    const r = await dockerRequest('/version', 'GET', null, 3000);
    dockerAvailable = r.status === 200;
  } catch { dockerAvailable = false; }
  console.log(dockerAvailable ? `[docker] 已连接 Engine API (${dockerPath})` : '[docker] 未检测到 Docker，进入 MOCK 模式');
  return dockerAvailable;
}

// docker logs 多路流拆包（非 TTY 时每帧前有 8 字节头）
function demuxDockerStream(buf) {
  if (buf.length < 8) return buf.toString('utf8');
  const out = [];
  let off = 0;
  while (off + 8 <= buf.length) {
    const size = buf.readUInt32BE(off + 4);
    const start = off + 8;
    out.push(buf.slice(start, Math.min(start + size, buf.length)).toString('utf8'));
    off = start + size;
  }
  return out.join('');
}

// ---------- Mock 数据 ----------
function mockContainers() {
  const mk = (name, image, state, status, ports, cpu, mem) => ({
    Id: 'mock' + Math.abs(hash(name)).toString(16).padStart(12, '0'),
    Names: ['/' + name], Image: image, State: state, Status: status,
    Ports: ports, Created: Math.floor(Date.now() / 1000) - Math.abs(hash(name)) % 900000,
    _mockStats: cpu !== undefined ? { cpu, mem } : null,
  });
  return [
    mk('docker-control', 'docker-control:latest', 'running', 'Up 2 hours', [{ PrivatePort: 33335, PublicPort: 33335, Type: 'tcp' }], 1.2, 128),
    mk('nginx-proxy', 'nginx:1.27-alpine', 'running', 'Up 3 days', [{ PrivatePort: 80, PublicPort: 80, Type: 'tcp' }, { PrivatePort: 443, PublicPort: 443, Type: 'tcp' }], 0.8, 56),
    mk('postgres', 'postgres:16', 'running', 'Up 3 days', [{ PrivatePort: 5432, PublicPort: 5432, Type: 'tcp' }], 3.4, 412),
    mk('redis', 'redis:7-alpine', 'running', 'Up 3 days', [{ PrivatePort: 6379, Type: 'tcp' }], 0.5, 24),
    mk('portainer', 'portainer/portainer-ce:latest', 'running', 'Up 12 days', [{ PrivatePort: 9443, PublicPort: 9443, Type: 'tcp' }], 0.6, 92),
    mk('watchtower', 'containrrr/watchtower', 'exited', 'Exited (0) 5 hours ago', [], null, null),
    mk('old-builder', 'golang:1.22', 'exited', 'Exited (137) 2 days ago', [], null, null),
    mk('jellyfin', 'jellyfin/jellyfin:latest', 'paused', 'Up 9 days (Paused)', [{ PrivatePort: 8096, PublicPort: 8096, Type: 'tcp' }], 2.1, 356),
  ];
}
function hash(s) { let h = 0; for (const c of s) h = (h * 31 + c.charCodeAt(0)) | 0; return h; }
function mockLogs(name) {
  const lines = [
    `[${new Date().toISOString()}] ${name} | mock 日志输出（未连接 Docker 时的演示数据）`,
    `[${new Date().toISOString()}] ${name} | listening on 0.0.0.0:8080`,
    `[${new Date().toISOString()}] ${name} | GET /api/health 200 3ms`,
    `[${new Date().toISOString()}] ${name} | level=info msg="background sync completed"`,
  ];
  return Array.from({ length: 30 }, (_, i) => lines[i % lines.length]).join('\n');
}

// ---------- API 路由 ----------
async function listContainers() {
  const r = await dockerRequest('/containers/json?all=1');
  return JSON.parse(r.buf.toString('utf8'));
}
async function hostStats() {
  const r = await dockerRequest('/info');
  return JSON.parse(r.buf.toString('utf8'));
}
async function containerStatsOne(id) {
  const r = await dockerRequest(`/containers/${id}/stats?stream=false`, 'GET', null, 8000);
  return JSON.parse(r.buf.toString('utf8'));
}
function calcCpuPercent(s) {
  const cd = s.cpu_stats.cpu_usage.total_usage - s.precpu_stats.cpu_usage.total_usage;
  const sd = s.cpu_stats.system_cpu_usage - s.precpu_stats.system_cpu_usage;
  if (sd <= 0 || cd < 0) return 0;
  const n = (s.cpu_stats.online_cpus || (s.cpu_stats.cpu_usage.percpu_usage || []).length || 1);
  return (cd / sd) * n * 100;
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const p = url.pathname;
  try {
    // ---- 静态文件 ----
    if (req.method === 'GET' && !p.startsWith('/api/')) {
      let file = p === '/' ? '/index.html' : p;
      file = path.normalize(path.join(PUBLIC_DIR, file));
      if (!file.startsWith(PUBLIC_DIR)) { res.writeHead(403); return res.end(); }
      const mime = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2' };
      try {
        const data = fs.readFileSync(file);
        res.writeHead(200, { 'Content-Type': mime[path.extname(file)] || 'application/octet-stream' });
        return res.end(data);
      } catch { res.writeHead(404); return res.end('Not Found'); }
    }

    const live = await detectDocker();

    // ---- 系统信息 ----
    if (p === '/api/system') {
      if (!live) return json(res, { mode: 'mock', version: { Version: '27.0-mock', ApiVersion: '1.43' }, host: { NCPU: 8, MemTotal: 16 * 1024 ** 3, Name: 'MOCK-HOST' } });
      const [v, h] = await Promise.all([dockerRequest('/version'), dockerRequest('/info')]);
      return json(res, { mode: 'live', version: JSON.parse(v.buf), host: JSON.parse(h.buf) });
    }

    // ---- 容器列表 ----
    if (p === '/api/containers' && req.method === 'GET') {
      let list = live ? await listContainers() : mockContainers();
      if (live) {
        // 附带一次性 stats（仅运行中的，逐个取，容忍失败）
        list = await Promise.all(list.map(async (c) => {
          if (c.State !== 'running') return { ...c, _mockStats: null };
          try { const s = await containerStatsOne(c.Id); return { ...c, _mockStats: { cpu: +calcCpuPercent(s).toFixed(1), mem: Math.round((s.memory_stats.usage || 0) / 1048576) } }; }
          catch { return { ...c, _mockStats: null }; }
        }));
      }
      return json(res, list.map((c) => ({
        id: c.Id, name: (c.Names && c.Names[0] || '').replace(/^\//, ''), image: c.Image,
        state: c.State, status: c.Status, ports: c.Ports || [], created: c.Created,
        cpu: c._mockStats ? c._mockStats.cpu : null, mem: c._mockStats ? c._mockStats.mem : null,
      })));
    }

    // ---- 容器操作 ----
    const mAct = p.match(/^\/api\/containers\/([a-f0-9]{12,}|[\w\-\.]+)\/(start|stop|restart|remove)$/);
    if (mAct && req.method === 'POST') {
      const [, id, act] = mAct;
      if (!live) return json(res, { ok: true, mock: true, action: act });
      const t = act === 'remove' ? 'DELETE' : 'POST';
      const r = await dockerRequest(`/containers/${id}?${act === 'remove' ? 'force=true&v=true' : ''}`, t, null, 30000);
      return json(res, { ok: r.status < 300 || r.status === 304 || r.status === 404, status: r.status });
    }

    // ---- 容器日志 ----
    const mLog = p.match(/^\/api\/containers\/([a-f0-9]{12,}|[\w\-\.]+)\/logs$/);
    if (mLog) {
      const tail = url.searchParams.get('tail') || '200';
      if (!live) return res.end(mockLogs(mLog[1]));
      const r = await dockerRequest(`/containers/${mLog[1]}/logs?stdout=1&stderr=1&tail=${tail}&timestamps=1`);
      return res.end(demuxDockerStream(r.buf));
    }

    // ---- 镜像列表 ----
    if (p === '/api/images') {
      if (!live) return json(res, [
        { Id: 'sha256:mock1', RepoTags: ['docker-control:latest'], Size: 68 * 1048576, Created: Date.now() / 1000 | 0 },
        { Id: 'sha256:mock2', RepoTags: ['nginx:1.27-alpine'], Size: 43 * 1048576, Created: Date.now() / 1000 | 0 },
        { Id: 'sha256:mock3', RepoTags: ['postgres:16'], Size: 431 * 1048576, Created: Date.now() / 1000 | 0 },
        { Id: 'sha256:mock4', RepoTags: ['redis:7-alpine'], Size: 41 * 1048576, Created: Date.now() / 1000 | 0 },
      ]);
      const r = await dockerRequest('/images/json');
      const list = JSON.parse(r.buf.toString('utf8'));
      return json(res, list.filter((i) => i.RepoTags && i.RepoTags.length).map((i) => ({ id: i.Id, tags: i.RepoTags, size: i.Size, created: i.Created })));
    }

    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: 'not found' }));
  } catch (e) {
    console.error('[error]', p, e.message);
    res.writeHead(500, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: e.message }));
  }
});

function json(res, obj) {
  res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' });
  res.end(JSON.stringify(obj));
}

server.listen(PORT, () => {
  console.log(`[panel] Docker Control Panel -> http://localhost:${PORT}`);
  detectDocker();
});
