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
const minimapCanvas = document.getElementById('minimap');
const wctx = worldCanvas.getContext('2d');
const cctx = chartCanvas.getContext('2d');
const mctx = minimapCanvas.getContext('2d');
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
// 视口状态：zoom 缩放倍率（1=全貌，>1 放大），cx/cy 视口中心的网格坐标（浮点）。
let view = { zoom: 1, cx: 0, cy: 0 };

// 按容器实际大小自适应 canvas（保持正方形），内部用固定高分辨率保证放大清晰。
function resizeWorldCanvas() {
  const wrap = worldCanvas.parentElement;
  if (!wrap) return;
  const avail = Math.min(wrap.clientWidth, wrap.clientHeight);
  const size = Math.max(120, Math.floor(avail));
  worldCanvas.style.width = size + 'px';
  worldCanvas.style.height = size + 'px';
  // 内部像素固定 1024×1024，放大时格子仍清晰（不随网格尺寸变化）
  const px = 1024;
  if (worldCanvas.width !== px || worldCanvas.height !== px) {
    worldCanvas.width = px;
    worldCanvas.height = px;
  }
}

// renderWorld(snap, t)：t 为 0~1 的插值因子，用于在 prevSnapshot→snapshot 间平滑移动动物/尸体，
// 以及平滑过渡草地/河流颜色。支持视口缩放/平移：只渲染视口内的格子。
function renderWorld(snap, t) {
  const gw = snap.width || GRID;
  const gh = snap.height || GRID;
  resizeWorldCanvas();
  const w = worldCanvas.width, h = worldCanvas.height;
  const k = t === undefined ? 1 : t;

  // 视口计算：zoom 决定可见网格数，cx/cy 决定视口中心
  const zoom = Math.max(1, view.zoom);
  const visW = gw / zoom;   // 水平可见网格数
  const visH = gh / zoom;   // 垂直可见网格数
  // 钳制视口中心，避免平移出界
  view.cx = Math.min(Math.max(view.cx, visW / 2), gw - visW / 2);
  view.cy = Math.min(Math.max(view.cy, visH / 2), gh - visH / 2);
  // 视口覆盖的网格范围（浮点边界）
  const vx0 = view.cx - visW / 2;
  const vy0 = view.cy - visH / 2;
  // 格子像素大小：画布像素 / 可见网格数（放大时格子变大）
  const cellW = w / visW;
  const cellH = h / visH;

  wctx.clearRect(0, 0, w, h);
  drawTerrain(snap, k, gw, gh, w, h, cellW, cellH, vx0, vy0);
  drawActors(snap, k, cellW, cellH, vx0, vy0);
  renderMinimap(snap, gw, gh);
}

// 雨点：在格子像素渲染时叠加——每个草地格根据降雨量，在格内画闪烁的蓝色点表示下雨。
// 融入 drawTerrain 的像素写入，格子级别的雨点效果（不是独立图层）。
// 闪烁由时间相位驱动：降雨量越大，闪烁的格子越多、雨点越亮。

// 绘制地形背景（草/河流的像素块）。草颜色受「草量（深浅）+ 湿度（色相绿↔黄）」双重影响：
// 湿季葱绿、旱季枯黄，连续渐变。k 为插值因子，用于在 prev→current 快照间平滑过渡
// 降雨量/水量/草量，避免颜色跳变闪烁。
function drawTerrain(snap, k, gw, gh, w, h, cellW, cellH, vx0, vy0) {
  const img = wctx.createImageData(w, h);
  const data = img.data;
  const terrain = snap.terrain || null;
  const water = snap.water || null;
  const prevWater = prevSnapshot && prevSnapshot.water ? prevSnapshot.water : null;
  const prevGrass = prevSnapshot && prevSnapshot.grass ? prevSnapshot.grass : null;
  // 湿度：降雨量 0~4mm/tick 映射到 0~1（4 为饱和），在 prev→current 间插值
  const prevRain = prevSnapshot && prevSnapshot.rainfall != null ? prevSnapshot.rainfall : snap.rainfall;
  const curRain = snap.rainfall != null ? snap.rainfall : prevRain;
  const rain = prevRain + (curRain - prevRain) * k;
  const wet = Math.max(0, Math.min(1, rain / 4));
  // 雨点闪烁：每个格子用独立频率与相位（多频叠加），产生自然不规则的明暗变化，
  // 避免全局同步的脉冲式闪烁。
  const now = performance.now() * 0.001; // 秒
  // 视口覆盖的整数格子范围（只渲染可见格子）
  const gx0 = Math.max(0, Math.floor(vx0));
  const gy0 = Math.max(0, Math.floor(vy0));
  const gx1 = Math.min(gw, Math.ceil(vx0 + w / cellW));
  const gy1 = Math.min(gh, Math.ceil(vy0 + h / cellH));
  for (let gy = gy0; gy < gy1; gy++) {
    for (let gx = gx0; gx < gx1; gx++) {
      const idx = gy * gw + gx;
      // 草量/水量在 prev→current 间插值
      const grass = prevGrass
        ? (prevGrass[idx] || 0) + ((snap.grass[idx] || 0) - (prevGrass[idx] || 0)) * k
        : (snap.grass[idx] || 0);
      const wl = water
        ? (prevWater ? (prevWater[idx] || 0) + (water[idx] - (prevWater[idx] || 0)) * k : water[idx])
        : 100;
      const nutrient = snap.nutrient[idx] || 0;
      let r, gg, b;
      if (terrain && terrain[idx] === 1) {
        // 河流格：按水量着色——水量高=深蓝，水量低=泥褐色（淤泥），
        // 中间为蓝褐色连续过渡（不再有浅蓝阶段）。
        const s = Math.max(0, Math.min(1, wl / 100)); // 0~1，水量越高越接近深蓝
        // 深蓝(水量100) ~ (20,60,130)，泥褐(水量0) ~ (90,70,45)
        r = 90 - s * 70;
        gg = 70 - s * 10;
        b = 45 + s * 85;
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
      // 像素坐标 = 网格坐标减去视口偏移，再乘以格子大小
      const x0 = Math.round((gx - vx0) * cellW);
      const y0 = Math.round((gy - vy0) * cellH);
      const x1 = Math.round((gx + 1 - vx0) * cellW);
      const y1 = Math.round((gy + 1 - vy0) * cellH);

      // 雨点：仅在草地格（非河流）叠加，格子中心附近一个闪烁的蓝色点表示下雨。
      // 用格子坐标哈希得到确定性随机，结合时间相位和降雨量决定该格此刻是否闪雨点。
      let dropOn = false;
      let dropR = 0, dropG = 0, dropB = 0, dropCx = 0, dropCy = 0, dropR2 = 0;
      if (terrain[idx] !== 1 && rain > 1.0) {
        // 确定性哈希：格子坐标 → 0~1 稳定随机
        const h = ((gx * 73856093) ^ (gy * 19349663)) >>> 0;
        const cellRand = (h % 1000) / 1000;
        // 降雨量决定活跃比例：rain/12（密度减半），饱和上限 0.33，避免整图铺满
        const active = cellRand < Math.min(0.33, rain / 12);
        if (active) {
          // 每个格子独立频率与相位，两个正弦相乘形成不规则包络（拍频），
          // 产生自然的"时亮时暗"闪烁，而非全局同步脉冲。
          const f1 = 1.5 + cellRand * 3.5;           // 第一频率 1.5~5 Hz
          const f2 = 4.0 + cellRand * 6.0;           // 第二频率 4~10 Hz
          const p1 = cellRand * 6.283;               // 独立相位
          const p2 = (cellRand * 7.3) % 6.283;
          const wave = Math.sin(now * f1 * 6.283 + p1) * Math.sin(now * f2 * 6.283 + p2);
          // 归一化到 0~1，只有正半波较强时才显示雨点
          const bright = Math.max(0, wave);
          if (bright > 0.35) {
            dropOn = true;
            const a = bright;
            dropR = 70 + 40 * a;    // 柔和蓝
            dropG = 120 + 30 * a;
            dropB = 200;
            dropCx = (x0 + x1) / 2;
            dropCy = (y0 + y1) / 2;
            const rr = Math.max(1, cellW * 0.16);
            dropR2 = rr * rr;
          }
        }
      }

      for (let py = y0; py < y1; py++) {
        for (let px = x0; px < x1; px++) {
          const o = (py * w + px) * 4;
          if (dropOn) {
            const dx = px - dropCx;
            const dy = py - dropCy;
            if (dx * dx + dy * dy <= dropR2) {
              data[o] = dropR;
              data[o + 1] = dropG;
              data[o + 2] = dropB;
              data[o + 3] = 255;
              continue;
            }
          }
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
// vx0/vy0 为视口左上角网格坐标，坐标减去偏移后映射到画布。
function drawActors(snap, t, cellW, cellH, vx0, vy0) {
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
      const px = (cx - vx0) * cellW + cellW / 2;
      const py = (cy - vy0) * cellH + cellH / 2;
      
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
      const px = (e.x - vx0) * cellW + cellW / 2;
      const py = (e.y - vy0) * cellH + cellH / 2;
      
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
    const px = (ax - vx0) * cellW + cellW / 2;
    const py = (ay - vy0) * cellH + cellH / 2;
    
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

// 渲染缩略图：显示完整世界 + 当前视口矩形框。仅放大（zoom>1）时显示。
function renderMinimap(snap, gw, gh) {
  const show = view.zoom > 1.01;
  minimapCanvas.style.display = show ? 'block' : 'none';
  if (!show) return;

  const mw = minimapCanvas.width, mh = minimapCanvas.height;
  // 用当前快照数据绘制低分辨率缩略图（草量+河流+淤泥）
  const terrain = snap.terrain || null;
  const grass = snap.grass || null;
  const water = snap.water || null;
  for (let y = 0; y < mh; y++) {
    for (let x = 0; x < mw; x++) {
      const gx = Math.floor(x / mw * gw);
      const gy = Math.floor(y / mh * gh);
      const idx = gy * gw + gx;
      let r, g, b;
      if (terrain && terrain[idx] === 1) {
        const wl = water ? water[idx] : 100;
        if (wl < 15) { r = 90; g = 70; b = 45; }      // 淤泥
        else { r = 20; g = 60; b = 130; }              // 深蓝
      } else {
        const gr = Math.min(1, (grass[idx] || 0) / 100);
        r = 30 + gr * 60;
        g = 80 + gr * 90;
        b = 30 + gr * 20;
      }
      mctx.fillStyle = `rgb(${r},${g},${b})`;
      mctx.fillRect(x, y, 1, 1);
    }
  }
  // 视口矩形框
  const zoom = Math.max(1, view.zoom);
  const vw = gw / zoom, vh = gh / zoom;
  const vx0 = view.cx - vw / 2, vy0 = view.cy - vh / 2;
  const rx = vx0 / gw * mw;
  const ry = vy0 / gh * mh;
  const rw = vw / gw * mw;
  const rh = vh / gh * mh;
  mctx.strokeStyle = '#ffffff';
  mctx.lineWidth = 1.5;
  mctx.strokeRect(rx, ry, rw, rh);
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

// ---------- 视口缩放/平移 ----------
// 滚轮缩放：围绕鼠标位置缩放，zoom 范围 1~16
worldCanvas.addEventListener('wheel', (e) => {
  e.preventDefault();
  if (!snapshot) return;
  const gw = snapshot.width || GRID;
  const gh = snapshot.height || GRID;
  const rect = worldCanvas.getBoundingClientRect();
  // 鼠标在画布上的比例位置 → 对应网格坐标
  const mx = (e.clientX - rect.left) / rect.width;
  const my = (e.clientY - rect.top) / rect.height;
  const visW = gw / Math.max(1, view.zoom);
  const visH = gh / Math.max(1, view.zoom);
  const gridX = view.cx - visW / 2 + mx * visW;
  const gridY = view.cy - visH / 2 + my * visH;
  // 缩放
  const factor = e.deltaY < 0 ? 1.25 : 0.8;
  view.zoom = Math.min(16, Math.max(1, view.zoom * factor));
  // 保持鼠标指向的网格点不动
  const newVisW = gw / view.zoom;
  const newVisH = gh / view.zoom;
  view.cx = gridX - (mx - 0.5) * newVisW;
  view.cy = gridY - (my - 0.5) * newVisH;
  if (snapshot) renderWorld(snapshot, 1);
}, { passive: false });

// 拖拽平移
let dragging = false;
let dragStartX = 0, dragStartY = 0, dragStartCx = 0, dragStartCy = 0;
worldCanvas.addEventListener('mousedown', (e) => {
  dragging = true;
  dragStartX = e.clientX;
  dragStartY = e.clientY;
  dragStartCx = view.cx;
  dragStartCy = view.cy;
  worldCanvas.style.cursor = 'grabbing';
});
window.addEventListener('mousemove', (e) => {
  if (!dragging || !snapshot) return;
  const gw = snapshot.width || GRID;
  const gh = snapshot.height || GRID;
  const rect = worldCanvas.getBoundingClientRect();
  const visW = gw / view.zoom;
  const visH = gh / view.zoom;
  // 鼠标移动的屏幕像素 → 网格距离
  const dx = (e.clientX - dragStartX) / rect.width * visW;
  const dy = (e.clientY - dragStartY) / rect.height * visH;
  view.cx = dragStartCx - dx;
  view.cy = dragStartCy - dy;
  if (snapshot) renderWorld(snapshot, 1);
});
window.addEventListener('mouseup', () => {
  dragging = false;
  worldCanvas.style.cursor = 'grab';
});
worldCanvas.style.cursor = 'grab';

// 缩略图点击跳转视口
minimapCanvas.addEventListener('click', (e) => {
  if (!snapshot) return;
  const gw = snapshot.width || GRID;
  const gh = snapshot.height || GRID;
  const rect = minimapCanvas.getBoundingClientRect();
  const fx = (e.clientX - rect.left) / rect.width;
  const fy = (e.clientY - rect.top) / rect.height;
  view.cx = fx * gw;
  view.cy = fy * gh;
  if (snapshot) renderWorld(snapshot, 1);
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
