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

async function doReset(seed) {
  stop();
  busy = true;
  try {
    const url = seed != null ? `/api/reset?seed=${encodeURIComponent(seed)}` : '/api/reset';
    const snap = await api(url, { method: 'POST' });
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
        history.push({ tick: s.tick, deer: s.deer_pop, tiger: s.tiger_pop, crocodile: s.crocodile_pop || 0 });
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
  let deer = 0, tiger = 0, crocodile = 0;
  for (const a of snap.animals) {
    if (a.species === 'deer') deer++;
    else if (a.species === 'tiger') tiger++;
    else if (a.species === 'crocodile') crocodile++;
  }
  let grass = 0;
  for (const g of snap.grass) grass += g;
  $('tick').textContent = snap.tick;
  $('seed').textContent = snap.seed;
  $('deer').textContent = deer;
  $('tiger').textContent = tiger;
  $('crocodile').textContent = crocodile;
  $('corpse').textContent = snap.corpses == null ? 0 : snap.corpses.length;
  $('grass').textContent = Math.round(grass);
  $('hash').textContent = snap.state_hash.toString(16).padStart(16, '0');
  $('season').textContent = seasonName(snap.season);
  $('temperature').textContent = snap.temperature != null ? snap.temperature.toFixed(1) + '℃' : '-';
  $('rainfall').textContent = snap.rainfall != null ? snap.rainfall.toFixed(1) + 'mm' : '-';
}

// 季节名（后端返回 wet_early/wet_peak/dry_early/dry_late）→ 中文
function seasonName(key) {
  const names = {
    wet_early: '湿季初期',
    wet_peak: '湿季盛期',
    dry_early: '旱季初期',
    dry_late: '旱季末期',
  };
  return names[key] || key || '-';
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
  drawRain(snap, w, h, cellW, cellH);
}

// 雨滴动画：降雨量越大，草地上蓝色小点越多（闪烁代表下雨）。
// 性能：用固定上限雨滴池 + 确定性伪随机位置，每帧只画应显示数量，无额外对象分配。
const RAIN_MAX = 200; // 雨滴池上限
function drawRain(snap, w, h, cellW, cellH) {
  const rainfall = snap.rainfall || 0;
  if (rainfall <= 0.05) return; // 几乎无雨则不画
  // 雨滴数随降雨量线性增长，饱和于 RAIN_MAX
  const count = Math.round(Math.min(1, rainfall / 4) * RAIN_MAX);
  if (count <= 0) return;
  const now = performance.now();
  wctx.fillStyle = 'rgba(120, 180, 255, 0.7)';
  const size = Math.max(1, cellW * 0.15);
  for (let i = 0; i < count; i++) {
    // 确定性伪随机位置：用 index 哈希，避免每帧重新随机导致雨滴跳变
    const hx = (i * 2654435761) % 1000000007;
    const hy = (i * 40503) % 1000000007;
    const x = (hx % 1000000007) / 1000000007 * w;
    const y = (hy % 1000000007) / 1000000007 * h;
    // 闪烁：按时间 + index 相位让雨滴明暗交替（雨滴下落感）
    const phase = (now * 0.01 + i * 0.7) % 1;
    wctx.globalAlpha = 0.25 + 0.5 * (0.5 + 0.5 * Math.sin(phase * Math.PI * 2));
    wctx.fillRect(x, y, size, size * 1.6);
  }
  wctx.globalAlpha = 1;
}

// 绘制地形背景（草/河流的像素块）。草颜色受「草量（深浅）+ 湿度（色相绿↔黄）」双重影响：
// 湿季葱绿、旱季枯黄，连续渐变。
function drawTerrain(snap, gw, gh, w, h, cellW, cellH) {
  const img = wctx.createImageData(w, h);
  const data = img.data;
  const terrain = snap.terrain || null;
  // 湿度：降雨量 0~4mm/tick 映射到 0~1（4 为饱和）
  const wet = snap.rainfall != null ? Math.max(0, Math.min(1, snap.rainfall / 4)) : 0.5;
  for (let gy = 0; gy < gh; gy++) {
    for (let gx = 0; gx < gw; gx++) {
      const idx = gy * gw + gx;
      const grass = snap.grass[idx] || 0;
      const nutrient = snap.nutrient[idx] || 0;
      let r, gg, b;
      if (terrain && terrain[idx] === 1) {
        r = 30; gg = 90; b = 180;
      } else {
        // 草量决定深浅（0~1），湿度决定色相（wet=1 葱绿，wet=0 枯黄）
        const g = Math.min(1, grass / 100);
        // 湿季绿 → 旱季黄 的基准色插值
        const greenR = 30, greenG = 120, greenB = 45;   // 葱绿
        const dryR = 170, dryG = 135, dryB = 45;        // 枯黄
        r = greenR + (dryR - greenR) * (1 - wet);
        gg = greenG + (dryG - greenG) * (1 - wet);
        b = greenB + (dryB - greenB) * (1 - wet);
        // 草量影响深浅：草少则偏暗
        r *= 0.4 + 0.6 * g;
        gg *= 0.4 + 0.6 * g;
        b *= 0.4 + 0.6 * g;
        // 养分叠加棕色
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

// 预加载 SVG 图片
const svgImages = {
  deer: new Image(),
  tiger: new Image(),
  crocodile: new Image(),
  egg: new Image(),
  corpse: new Image()
};

// 设置 SVG 图片路径
svgImages.deer.src = 'icon/鹿.svg';
svgImages.tiger.src = 'icon/虎.svg';
svgImages.crocodile.src = 'icon/鳄鱼.svg';
svgImages.egg.src = 'icon/鳄鱼蛋.svg';
svgImages.corpse.src = 'icon/尸体.svg';

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
      
      // 绘制尸体 SVG
      wctx.save();
      wctx.translate(px, py);
      wctx.scale(cellW * 0.01, cellW * 0.01); // 调整 SVG 大小比例
      wctx.drawImage(svgImages.corpse, -svgImages.corpse.width/2, -svgImages.corpse.height/2);
      wctx.restore();
    }
  }

  // 鳄鱼蛋（浅色小点）
  if (snap.eggs != null) {
    for (const e of snap.eggs) {
      const px = e.x * cellW + cellW / 2;
      const py = e.y * cellH + cellH / 2;
      
      // 绘制鳄鱼蛋 SVG
      wctx.save();
      wctx.translate(px, py);
      wctx.scale(cellW * 0.01, cellW * 0.01); // 调整 SVG 大小比例
      wctx.drawImage(svgImages.egg, -svgImages.egg.width/2, -svgImages.egg.height/2);
      wctx.restore();
    }
  }

  // 动物（SVG 图片，用高对比色区分鹿/虎）
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
    
    // 根据动物种类选择对应的 SVG
    let image;
    if (a.species === 'deer') {
      image = svgImages.deer;
    } else if (a.species === 'tiger') {
      image = svgImages.tiger;
    } else {
      image = svgImages.crocodile;
    }
    
    // 绘制动物 SVG
    wctx.save();
    wctx.translate(px, py);
    wctx.scale(cellW * 0.01, cellW * 0.01); // 调整 SVG 大小比例
    wctx.drawImage(image, -image.width/2, -image.height/2);
    wctx.restore();
  }
}

// ---------- 种群曲线 ----------
// 每个物种独立曲线，通过按钮切换当前显示物种
const SPECIES = [
  { key: 'deer', label: '鹿', color: '#00fce5' },
  { key: 'tiger', label: '虎', color: '#ff4400' },
  { key: 'crocodile', label: '鳄鱼', color: '#2e8b57' },
];
let activeSpecies = 'deer'; // 当前显示的物种曲线

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

  const sp = SPECIES.find((s) => s.key === activeSpecies) || SPECIES[0];
  const maxVal = Math.max(10, ...history.map((p) => p[sp.key] || 0));
  const minTick = history[0].tick;
  const maxTick = history[history.length - 1].tick;
  const span = Math.max(1, maxTick - minTick);

  const mapX = (tick) => ((tick - minTick) / span) * w;
  const mapY = (v) => h - (v / maxVal) * (h - 8) - 4;

  drawLine(sp.key, sp.color, mapX, mapY);

  // 曲线标题（左上角显示当前物种 + 最新数量）
  const latest = history[history.length - 1][sp.key] || 0;
  cctx.fillStyle = sp.color;
  cctx.font = '12px ui-monospace, monospace';
  cctx.fillText(`${sp.label}：${latest}`, 6, 14);
}

function drawLine(key, color, mapX, mapY) {
  cctx.strokeStyle = color;
  cctx.lineWidth = 1.5;
  cctx.beginPath();
  for (let i = 0; i < history.length; i++) {
    const p = history[i];
    const x = mapX(p.tick);
    const y = mapY(p[key] || 0);
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
btnReset.addEventListener('click', () => doReset());

// 种群曲线物种切换按钮
document.querySelectorAll('.chart-tab').forEach((btn) => {
  btn.addEventListener('click', () => {
    activeSpecies = btn.dataset.species;
    document.querySelectorAll('.chart-tab').forEach((b) => b.classList.remove('active'));
    btn.classList.add('active');
    renderChart();
  });
});
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

// ---------- 主界面：输入种子进入游戏 ----------
const startScreen = $('start-screen');
const seedInput = $('seed-input');
const startHint = $('start-hint');
const btnEnter = $('btn-enter');

async function enterGame() {
  let raw = seedInput.value.trim();
  let seed = null;
  if (raw !== '') {
    if (!/^\d+$/.test(raw)) {
      startHint.textContent = '种子必须是数字';
      startHint.className = 'start-hint error';
      return;
    }
    seed = raw;
  }
  startHint.textContent = '正在生成世界…';
  startHint.className = 'start-hint';
  try {
    // 用指定种子（或默认种子）重建世界
    if (seed != null) {
      await doReset(seed);
    } else {
      const snap = await api('/api/state');
      history = [];
      prevSnapshot = null;
      onSnapshot(snap);
    }
    startScreen.style.display = 'none';
  } catch (e) {
    startHint.textContent = '无法连接服务器: ' + e.message;
    startHint.className = 'start-hint error';
  }
}

btnEnter.addEventListener('click', enterGame);
seedInput.addEventListener('keydown', (e) => {
  if (e.code === 'Enter' || e.code === 'NumpadEnter') {
    enterGame();
  }
});
// 初始聚焦到种子输入框
seedInput.focus();
