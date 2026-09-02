'use strict';

// ---------- 状态 ----------
let snapshot = null;       // 当前世界快照（插值终点）
let prevSnapshot = null;   // 上一帧世界快照（插值起点）
let animStart = 0;         // 本次插值动画的起始时间戳（ms）
let history = [];          // 种群历史（从 samples 累积）
let running = false;       // 是否在播放
let rafId = null;          // requestAnimationFrame id
let busy = false;          // 是否有请求进行中

// ---------- DOM ----------
const worldCanvas = document.getElementById('world');
const chartCanvas = document.getElementById('chart');
const wctx = worldCanvas.getContext('2d');
const cctx = chartCanvas.getContext('2d');
const $ = (id) => document.getElementById(id);

const btnPlay = $('btn-play');
const btnStep = $('btn-step');
const btnReset = $('btn-reset');
const speedSel = $('speed');
const statusEl = $('status');
const eventsEl = $('events');

// ---------- 常量 ----------
const GRID = 64; // 与后端一致（Snapshot.width/height）

// ---------- API ----------
async function api(path, opts) {
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const err = new Error(data && data.error ? data.error : `HTTP ${res.status}`);
    err.snapshot = data && data.snapshot ? data.snapshot : null;
    throw err;
  }
  return data;
}

async function fetchState() {
  const snap = await api('/api/state');
  onSnapshot(snap);
}

async function doStep() {
  if (busy) return;
  busy = true;
  try {
    const snap = await api('/api/step', { method: 'POST' });
    onSnapshot(snap);
  } catch (e) {
    handleError(e);
  } finally {
    busy = false;
  }
}

async function doRun(n) {
  if (busy) return;
  busy = true;
  try {
    const snap = await api(`/api/run?n=${n}`, { method: 'POST' });
    onSnapshot(snap);
  } catch (e) {
    handleError(e);
  } finally {
    busy = false;
  }
}

async function doReset() {
  stop();
  busy = true;
  try {
    const snap = await api('/api/reset', { method: 'POST' });
    history = [];
    prevSnapshot = null; // 重置：不保留旧世界坐标，避免跨世界插值
    onSnapshot(snap);
    setStatus('已重置');
  } catch (e) {
    handleError(e);
  } finally {
    busy = false;
  }
}

function handleError(e) {
  setStatus(e.message, true);
  if (e.snapshot) {
    onSnapshot(e.snapshot);
  }
  stop();
}

// ---------- 快照处理 ----------
function onSnapshot(snap) {
  // 累积种群历史（用后端返回的 samples，避免前端自行累积误差）
  if (snap.samples && snap.samples.length) {
    // 只保留 tick 递增的部分，去重
    for (const s of snap.samples) {
      if (history.length === 0 || s.tick > history[history.length - 1].tick) {
        history.push({ tick: s.tick, deer: s.deer_pop, tiger: s.tiger_pop });
      }
    }
    // 限制历史长度
    if (history.length > 2000) {
      history = history.slice(history.length - 2000);
    }
  }
  // 平滑移动：旧快照成为插值起点，新快照成为终点，动画从此刻开始
  prevSnapshot = snapshot;
  snapshot = snap;
  animStart = performance.now();
  renderStats(snap);
  renderChart();
  renderEvents(snap);
  setStatus('');
  if (!running) {
    renderWorld(snap, 1); // 未播放时直接画终态（无动画）
  }
}

function renderStats(snap) {
  let deer = 0, tiger = 0;
  for (const a of snap.animals) {
    if (a.species === 'deer') deer++;
    else if (a.species === 'tiger') tiger++;
  }
  let grass = 0;
  for (const g of snap.grass) grass += g;
  $('tick').textContent = snap.tick;
  $('deer').textContent = deer;
  $('tiger').textContent = tiger;
  $('corpse').textContent = snap.corpses == null ? 0 : snap.corpses.length;
  $('grass').textContent = Math.round(grass);
  $('hash').textContent = snap.state_hash.toString(16).padStart(16, '0');
  $('season').textContent = seasonName(snap.tick);
  $('weather').textContent = snap.weather ? snap.weather.current : '-';
}

function seasonName(tick) {
  const perSeason = 100; // 与后端配置一致（默认），这里做近似展示
  return ['春', '夏', '秋', '冬'][Math.floor(tick / perSeason) % 4];
}

// ---------- 世界网格渲染 ----------
// 按容器实际大小自适应 canvas（保持正方形），避免超出屏幕/显示不全
function resizeWorldCanvas(gw, gh) {
  const wrap = worldCanvas.parentElement;
  if (!wrap) return;
  const avail = Math.min(wrap.clientWidth, wrap.clientHeight);
  const size = Math.max(120, Math.floor(avail));
  worldCanvas.style.width = size + 'px';
  worldCanvas.style.height = size + 'px';
  // 内部像素取网格尺寸的整数倍，保证格子不模糊
  const scale = Math.max(1, Math.floor(size / Math.max(gw, gh)));
  const px = Math.max(gw, gh) * scale;
  if (worldCanvas.width !== px || worldCanvas.height !== px) {
    worldCanvas.width = px;
    worldCanvas.height = px;
  }
}

// renderWorld(snap, t)：t 为 0~1 的插值因子，用于在 prevSnapshot→snapshot 间平滑移动动物/尸体。
// t=1 时等价于直接画快照终态。每帧先彻底清屏，再完整重绘背景 + actors，杜绝拖影。
function renderWorld(snap, t) {
  const gw = snap.width || GRID;    // 用后端返回的真实宽高
  const gh = snap.height || GRID;
  resizeWorldCanvas(gw, gh);
  const w = worldCanvas.width, h = worldCanvas.height;
  const cellW = w / gw;
  const cellH = h / gh;

  // 彻底清屏（覆盖 putImageData 可能的 1px 舍入缝隙）
  wctx.clearRect(0, 0, w, h);
  drawTerrain(snap, gw, gh, w, h, cellW, cellH);
  drawActors(snap, t === undefined ? 1 : t, cellW, cellH);
}

// 绘制地形背景（草/河流的像素块）
function drawTerrain(snap, gw, gh, w, h, cellW, cellH) {
  const img = wctx.createImageData(w, h);
  const data = img.data;
  const terrain = snap.terrain || null;
  for (let gy = 0; gy < gh; gy++) {
    for (let gx = 0; gx < gw; gx++) {
      const idx = gy * gw + gx;
      const grass = snap.grass[idx] || 0;
      const nutrient = snap.nutrient[idx] || 0;
      let r, gg, b;
      if (terrain && terrain[idx] === 1) {
        r = 30; gg = 90; b = 180;
      } else {
        const g = Math.min(1, grass / 100);
        r = 18 + g * 40;
        gg = 60 + g * 120;
        b = 20 + g * 30;
        const n = Math.min(1, nutrient / 100);
        r += n * 40;
        gg *= (1 - n * 0.3);
        b *= (1 - n * 0.4);
        r = Math.min(255, r); gg = Math.min(255, gg); b = Math.min(255, b);
      }
      const x0 = Math.round(gx * cellW);
      const y0 = Math.round(gy * cellH);
      const x1 = Math.round((gx + 1) * cellW);
      const y1 = Math.round((gy + 1) * cellH);
      for (let py = y0; py < y1; py++) {
        for (let px = x0; px < x1; px++) {
          const o = (py * w + px) * 4;
          data[o] = r;
          data[o + 1] = gg;
          data[o + 2] = b;
          data[o + 3] = 255;
        }
      }
    }
  }
  wctx.putImageData(img, 0, 0);
}

// 按 ID 建立 prev 动物坐标索引，用于插值
function indexById(list) {
  const m = new Map();
  for (const a of list) {
    m.set(a.id, a);
  }
  return m;
}

// 绘制尸体和动物（t 为插值因子，0=上一快照位置，1=当前快照位置）
function drawActors(snap, t, cellW, cellH) {
  // 尸体（灰色小点）：同样按 ID 插值
  if (snap.corpses != null) {
    const prevC = prevSnapshot && prevSnapshot.corpses ? indexById(prevSnapshot.corpses) : null;
    for (const c of snap.corpses) {
      let cx = c.x, cy = c.y;
      if (prevC && prevC.has(c.id)) {
        const p = prevC.get(c.id);
        cx = p.x + (c.x - p.x) * t;
        cy = p.y + (c.y - p.y) * t;
      }
      const px = cx * cellW + cellW / 2;
      const py = cy * cellH + cellH / 2;
      wctx.fillStyle = '#8b949e';
      wctx.beginPath();
      wctx.arc(px, py, Math.max(2, cellW * 0.3), 0, Math.PI * 2);
      wctx.fill();
    }
  }

  // 动物（圆点，用高对比色 + 白描边区分鹿/虎）
  const prevA = prevSnapshot ? indexById(prevSnapshot.animals) : null;
  for (const a of snap.animals) {
    let ax = a.x, ay = a.y;
    if (prevA && prevA.has(a.id)) {
      const p = prevA.get(a.id);
      ax = p.x + (a.x - p.x) * t;
      ay = p.y + (a.y - p.y) * t;
    }
    const px = ax * cellW + cellW / 2;
    const py = ay * cellH + cellH / 2;
    const mature = a.age >= a.mature_age;
    const radius = Math.max(2.5, cellW * (mature ? 0.45 : 0.32));
    const color = a.species === 'deer' ? (mature ? '#c98a4b' : '#e6c9a8')
      : (mature ? '#ff6b35' : '#ffb38a');
    wctx.fillStyle = '#ffffff';
    wctx.beginPath();
    wctx.arc(px, py, radius + 1, 0, Math.PI * 2);
    wctx.fill();
    wctx.fillStyle = color;
    wctx.beginPath();
    wctx.arc(px, py, radius, 0, Math.PI * 2);
    wctx.fill();
  }
}

// ---------- 种群曲线 ----------
function renderChart() {
  const w = chartCanvas.width, h = chartCanvas.height;
  cctx.clearRect(0, 0, w, h);

  // 网格
  cctx.strokeStyle = '#21262d';
  cctx.lineWidth = 1;
  for (let i = 1; i < 4; i++) {
    const y = (h / 4) * i;
    cctx.beginPath(); cctx.moveTo(0, y); cctx.lineTo(w, y); cctx.stroke();
  }

  if (history.length < 2) return;

  const maxVal = Math.max(10, ...history.map((p) => Math.max(p.deer, p.tiger)));
  const minTick = history[0].tick;
  const maxTick = history[history.length - 1].tick;
  const span = Math.max(1, maxTick - minTick);

  const mapX = (tick) => ((tick - minTick) / span) * w;
  const mapY = (v) => h - (v / maxVal) * (h - 8) - 4;

  drawLine('deer', '#c98a4b', mapX, mapY);
  drawLine('tiger', '#e06c45', mapX, mapY);
}

function drawLine(key, color, mapX, mapY) {
  cctx.strokeStyle = color;
  cctx.lineWidth = 1.5;
  cctx.beginPath();
  for (let i = 0; i < history.length; i++) {
    const p = history[i];
    const x = mapX(p.tick);
    const y = mapY(p[key]);
    if (i === 0) cctx.moveTo(x, y);
    else cctx.lineTo(x, y);
  }
  cctx.stroke();
}

// ---------- 事件列表 ----------
function renderEvents(snap) {
  const events = snap.events || [];
  const recent = events.slice(-15).reverse();
  eventsEl.innerHTML = '';
  for (const ev of recent) {
    const li = document.createElement('li');
    li.textContent = `t${ev.tick} ${ev.type} A#${ev.a} B#${ev.b} v=${ev.val.toFixed(1)}`;
    eventsEl.appendChild(li);
  }
}

// ---------- 播放循环（按 tick/秒 控制节奏，慢速可看清每一步） ----------
// 每"帧"推进 1 个 tick，帧间隔 = 1000/ticksPerSec，因此速度直观可控。
let nextAt = 0;

function start() {
  if (running) return;
  running = true;
  btnPlay.textContent = '⏸ 暂停';
  nextAt = 0;
  loop();
}

function stop() {
  running = false;
  btnPlay.textContent = '▶ 开始';
  if (rafId) {
    cancelAnimationFrame(rafId);
    rafId = null;
  }
}

function toggle() {
  running ? stop() : start();
}

function loop() {
  if (!running) return;
  rafId = requestAnimationFrame(() => {
    if (!running) return;
    const now = performance.now();
    const tps = parseInt(speedSel.value, 10) || 30;
    const interval = 1000 / tps; // 每个 tick 的目标间隔（毫秒）

    // 平滑移动：每帧都在 prev→current 之间插值重绘，绝不被网络请求阻塞
    if (snapshot) {
      const t = animStart > 0 ? Math.min(1, (now - animStart) / interval) : 1;
      renderWorld(snapshot, t);
    }

    // tick 推进：到点就发起单步（fire-and-forget，不 await，避免阻塞动画帧）
    if (nextAt === 0) nextAt = now + interval;
    if (now >= nextAt) {
      nextAt = now + interval;
      if (!busy) {
        void doStep(); // 故意不 await：动画继续，快照到达后 onSnapshot 自动衔接
      }
    }
    loop();
  });
}

// ---------- 状态提示 ----------
function setStatus(msg, isError) {
  statusEl.textContent = msg || '就绪';
  statusEl.className = 'hint' + (isError ? ' error' : '');
}

// ---------- 绑定事件 ----------
btnPlay.addEventListener('click', toggle);
btnStep.addEventListener('click', doStep);
btnReset.addEventListener('click', doReset);
document.addEventListener('keydown', (e) => {
  if (e.code === 'Space') {
    e.preventDefault();
    toggle();
  } else if (e.code === 'KeyS') {
    doStep();
  }
});

// ---------- 窗口尺寸变化时重新自适应并重绘 ----------
window.addEventListener('resize', () => {
  if (snapshot) renderWorld(snapshot);
});

// ---------- 启动 ----------
fetchState().catch((e) => setStatus('无法连接服务器: ' + e.message, true));
