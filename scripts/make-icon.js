/**
 * 生成应用图标：build-resources/icon.ico（16/24/32/48/64 为 32 位 BMP，128/256 为 PNG）。
 * 纯 Node 实现，不依赖任何图像库；按尺寸原生绘制 + 4× 超采样抗锯齿。
 *
 * 图形：蓝色渐变圆角方块 + 白色房屋，屋内三根递增柱状（财产增长）。
 */
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');

// ---------- 颜色 ----------
const BG_TOP = [0x4c, 0x86, 0xff];
const BG_BOTTOM = [0x28, 0x4a, 0xcf];
const WHITE = [255, 255, 255];

function lerpColor(a, b, t) {
  return [
    Math.round(a[0] + (b[0] - a[0]) * t),
    Math.round(a[1] + (b[1] - a[1]) * t),
    Math.round(a[2] + (b[2] - a[2]) * t),
  ];
}

// ---------- 几何（坐标空间 0..256） ----------
function inRoundRect(ux, uy, x, y, w, h, r) {
  if (ux < x || ux > x + w || uy < y || uy > y + h) return false;
  const cx = Math.min(Math.max(ux, x + r), x + w - r);
  const cy = Math.min(Math.max(uy, y + r), y + h - r);
  const dx = ux - cx;
  const dy = uy - cy;
  return dx * dx + dy * dy <= r * r;
}

function inTriangle(ux, uy, ax, ay, bx, by, cx, cy) {
  const d1 = (ux - bx) * (ay - by) - (ax - bx) * (uy - by);
  const d2 = (ux - cx) * (by - cy) - (bx - cx) * (uy - cy);
  const d3 = (ux - ax) * (cy - ay) - (cx - ax) * (uy - ay);
  const hasNeg = d1 < 0 || d2 < 0 || d3 < 0;
  const hasPos = d1 > 0 || d2 > 0 || d3 > 0;
  return !(hasNeg && hasPos);
}

/** 屋顶（三角形）。 */
function inRoof(ux, uy) {
  return inTriangle(ux, uy, 128, 42, 28, 126, 228, 126);
}

/** 房体（圆角矩形）。 */
function inBody(ux, uy) {
  return inRoundRect(ux, uy, 60, 112, 136, 106, 16);
}

/** 屋内三根柱子（挖空，露出背景色）：顶部圆角、底部平齐。 */
const BARS = [
  { x: 78, h: 36 },
  { x: 114, h: 58 },
  { x: 150, h: 80 },
];
const BAR_W = 28;
const BAR_BASE = 204;

function inBar(ux, uy) {
  if (uy > BAR_BASE) return false;
  for (const bar of BARS) {
    const top = BAR_BASE - bar.h;
    if (inRoundRect(ux, uy, bar.x, top, BAR_W, bar.h + 8, 7)) return true;
  }
  return false;
}

// ---------- 渲染（4× 超采样） ----------
function renderRGBA(size) {
  const SS = 4;
  const S = size * SS;
  const k = S / 256;
  const hi = new Float32Array(S * S * 4);
  const simplified = size <= 24; // 小尺寸去掉柱子，保持清晰

  for (let y = 0; y < S; y++) {
    const uy = (y + 0.5) / k;
    const bg = lerpColor(BG_TOP, BG_BOTTOM, Math.min(Math.max(uy / 256, 0), 1));
    for (let x = 0; x < S; x++) {
      const ux = (x + 0.5) / k;
      const i = (y * S + x) * 4;
      let r = 0;
      let g = 0;
      let b = 0;
      let a = 0;
      if (inRoundRect(ux, uy, 0, 0, 256, 256, 54)) {
        // 左上角柔和高光，避免整块纯色显得扁平
        const d = Math.hypot(ux - 40, uy - 36);
        const hiLight = Math.max(0, 1 - d / 230) * 0.22;
        r = Math.round(bg[0] + (255 - bg[0]) * hiLight);
        g = Math.round(bg[1] + (255 - bg[1]) * hiLight);
        b = Math.round(bg[2] + (255 - bg[2]) * hiLight);
        a = 255;
        if (inRoof(ux, uy) || inBody(ux, uy)) {
          r = WHITE[0];
          g = WHITE[1];
          b = WHITE[2];
          if (!simplified && inBar(ux, uy)) {
            r = Math.round(bg[0] * 0.92);
            g = Math.round(bg[1] * 0.92);
            b = Math.round(bg[2] * 0.92);
          }
        }
      }
      hi[i] = r;
      hi[i + 1] = g;
      hi[i + 2] = b;
      hi[i + 3] = a;
    }
  }

  // 下采样（按 alpha 预乘，边缘过渡正确）
  const out = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let sr = 0;
      let sg = 0;
      let sb = 0;
      let sa = 0;
      for (let dy = 0; dy < SS; dy++) {
        for (let dx = 0; dx < SS; dx++) {
          const i = ((y * SS + dy) * S + (x * SS + dx)) * 4;
          const alpha = hi[i + 3];
          sr += hi[i] * alpha;
          sg += hi[i + 1] * alpha;
          sb += hi[i + 2] * alpha;
          sa += alpha;
        }
      }
      const o = (y * size + x) * 4;
      if (sa > 0) {
        out[o] = Math.round(sr / sa);
        out[o + 1] = Math.round(sg / sa);
        out[o + 2] = Math.round(sb / sa);
        out[o + 3] = Math.round(sa / (SS * SS));
      }
    }
  }
  return out;
}

// ---------- PNG 编码 ----------
const CRC_TABLE = (() => {
  const table = new Int32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c;
  }
  return table;
})();

function crc32(buf) {
  let c = -1;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ -1) >>> 0;
}

function pngChunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const typeBuf = Buffer.from(type, 'ascii');
  const crcBuf = Buffer.alloc(4);
  crcBuf.writeUInt32BE(crc32(Buffer.concat([typeBuf, data])));
  return Buffer.concat([len, typeBuf, data, crcBuf]);
}

function encodePng(rgba, width, height) {
  const sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 6;
  const raw = Buffer.alloc((width * 4 + 1) * height);
  for (let y = 0; y < height; y++) {
    raw[y * (width * 4 + 1)] = 0;
    rgba.copy(raw, y * (width * 4 + 1) + 1, y * width * 4, (y + 1) * width * 4);
  }
  return Buffer.concat([
    sig,
    pngChunk('IHDR', ihdr),
    pngChunk('IDAT', zlib.deflateSync(raw, { level: 9 })),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
}

// ---------- ICO 编码 ----------
/** 32 位 BMP（DIB）条目，兼容性最好。 */
function encodeDib(rgba, size) {
  const header = Buffer.alloc(40);
  header.writeUInt32LE(40, 0);
  header.writeInt32LE(size, 4);
  header.writeInt32LE(size * 2, 8); // XOR + AND
  header.writeUInt16LE(1, 12);
  header.writeUInt16LE(32, 14);
  header.writeUInt32LE(0, 16); // BI_RGB
  const xor = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y++) {
    const srcRow = size - 1 - y; // BMP 自下而上
    for (let x = 0; x < size; x++) {
      const s = (srcRow * size + x) * 4;
      const d = (y * size + x) * 4;
      xor[d] = rgba[s + 2];
      xor[d + 1] = rgba[s + 1];
      xor[d + 2] = rgba[s];
      xor[d + 3] = rgba[s + 3];
    }
  }
  const maskRow = Math.ceil(size / 32) * 4;
  const and = Buffer.alloc(maskRow * size); // 全 0：全部不透明
  header.writeUInt32LE(xor.length + and.length, 20);
  return Buffer.concat([header, xor, and]);
}

function buildIco(items) {
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(items.length, 4);
  const entries = [];
  let offset = 6 + items.length * 16;
  for (const item of items) {
    const entry = Buffer.alloc(16);
    entry[0] = item.size >= 256 ? 0 : item.size;
    entry[1] = item.size >= 256 ? 0 : item.size;
    entry[2] = 0;
    entry[3] = 0;
    entry.writeUInt16LE(1, 4);
    entry.writeUInt16LE(32, 6);
    entry.writeUInt32LE(item.data.length, 8);
    entry.writeUInt32LE(offset, 12);
    offset += item.data.length;
    entries.push(entry);
  }
  return Buffer.concat([header, ...entries, ...items.map((i) => i.data)]);
}

// ---------- 输出 ----------
const SIZES = [16, 24, 32, 48, 64, 128, 256];
const outDir = path.join(__dirname, '..', 'build-resources');
fs.mkdirSync(outDir, { recursive: true });

const items = [];
let rgba256 = null;
for (const size of SIZES) {
  const rgba = renderRGBA(size);
  if (size === 256) rgba256 = rgba;
  // 小尺寸用 BMP 保证各版本 Windows 正常显示；大尺寸用 PNG 压缩
  const data = size <= 64 ? encodeDib(rgba, size) : encodePng(rgba, size, size);
  items.push({ size, data });
  fs.writeFileSync(path.join(outDir, `icon-${size}.png`), encodePng(rgba, size, size));
}

const ico = buildIco(items);
fs.writeFileSync(path.join(outDir, 'icon.ico'), ico);

// 256 PNG 作为各处的通用图像资源
fs.writeFileSync(path.join(outDir, 'icon.png'), encodePng(rgba256, 256, 256));

console.log(`生成图标：${path.join(outDir, 'icon.ico')}`);
console.log(`  尺寸：${SIZES.join(' / ')}（16–64 为 BMP，128/256 为 PNG），共 ${ico.length} 字节`);
