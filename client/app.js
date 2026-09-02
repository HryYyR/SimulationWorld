'use strict';

// ---------- 状态 ----------
let snapshot = null;       // 当前世界快照
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
  snapshot = await api('/api/state');
  onSnapshot(snapshot);
}

async function doStep() {
  if (busy) return;
  busy = true;
  try {
    snapshot = await api('/api/step', { method: 'POST' });
    onSnapshot(snapshot);
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
    snapshot = await api(`/api/run?n=${n}`, { method: 'POST' });
    onSnapshot(snapshot);
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
    snapshot = await api('/api/reset', { method: 'POST' });
    history = [];
    onSnapshot(snapshot);
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
    snapshot = e.snapshot;
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
  renderStats(snap);
  renderWorld(snap);
  renderChart();
  renderEvents(snap);
  setStatus('');
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

function renderWorld(snap) {
  const gw = snap.width || GRID;    // 用后端返回的真实宽高
  const gh = snap.height || GRID;
  resizeWorldCanvas(gw, gh);
  const w = worldCanvas.width, h = worldCanvas.height;
  const cellW = w / gw;
  const cellH = h / gh;
  const img = wctx.createImageData(w, h);
  const data = img.data;

  // 逐像素填充：每个格子渲染成 cellW×cellH 的像素块
  const terrain = snap.terrain || null;
  for (let gy = 0; gy < gh; gy++) {
    for (let gx = 0; gx < gw; gx++) {
      const idx = gy * gw + gx;
      const grass = snap.grass[idx] || 0;
      const nutrient = snap.nutrient[idx] || 0;
      // 河流格（terrain==1）显示蓝色（河流与草互斥）
      let r, gg, b;
      if (terrain && terrain[idx] === 1) {
        r = 30; gg = 90; b = 180;
      } else {
        // 草量 0~100 映射到绿色亮度
        const g = Math.min(1, grass / 100);
        r = 18 + g * 40;
        gg = 60 + g * 120;
        b = 20 + g * 30;
        // 养分叠加棕色
        const n = Math.min(1, nutrient / 100);
        r += n * 40;
        gg *= (1 - n * 0.3);
        b *= (1 - n * 0.4);
        r = Math.min(255, r); gg = Math.min(255, gg); b = Math.min(255, b);
      }

      // 填充该格子对应的所有像素
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

  // 尸体（灰色小点）
  if (snap.corpses != null) {
    for (const c of snap.corpses) {
      const px = c.x * cellW + cellW / 2;
      const py = c.y * cellH + cellH / 2;
      wctx.fillStyle = '#8b949e';
      wctx.beginPath();
      wctx.arc(px, py, Math.max(2, cellW * 0.3), 0, Math.PI * 2);
      wctx.fill();
    }
  }

  // 动物（圆点，用高对比色 + 白描边区分鹿/虎）
  for (const a of snap.animals) {
    const px = a.x * cellW + cellW / 2;
    const py = a.y * cellH + cellH / 2;
    const mature = a.age >= a.mature_age;
    const radius = Math.max(2.5, cellW * (mature ? 0.45 : 0.32));
    // 鹿用暖棕、虎用亮橙红，两者色相差明显
    const color = a.species === 'deer' ? (mature ? '#c98a4b' : '#e6c9a8')
      : (mature ? '#ff6b35' : '#ffb38a');
    // 白描边增强辨识度
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
  rafId = requestAnimationFrame(async () => {
    if (!running) return;
    const now = performance.now();
    const tps = parseInt(speedSel.value, 10) || 30;
    const interval = 1000 / tps; // 每个 tick 的目标间隔（毫秒）
    if (nextAt === 0) nextAt = now + interval;

    if (now >= nextAt) {
      nextAt = now + interval;
      if (!busy) {
        await doStep(); // 每次只推进 1 tick，节奏稳定、看得清
      }
    }
    if (running) loop();
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
