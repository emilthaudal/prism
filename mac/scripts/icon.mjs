// Writes the art of Prism's app icon, "The split", as one full-bleed SVG for the macOS 26 .icon (Resources/Prism.icon):
// a glass prism on ink; one white beam enters from the left and leaves as the five lane colours.
// The drawing is pSplit from docs/design/prism-identity.html, where Emil picked it (2026-11-25), with two changes:
// - full bleed, no tile, sheen or edge, since macOS masks the square and adds its own glass;
// - a thicker outline (34) and beam (60), and the outline drawn last over the glass and the light wedge: the macOS 26
//   .icon format draws every size from this one picture (no per-size artwork), so following Apple's guidance the one
//   design carries few, bold shapes that still read at 16 px: darker glass and fainter background glows, so the white
//   outline and beam keep their contrast when the whole icon is 16 pixels.
// Usage: node scripts/icon.mjs OUT.svg
import { writeFileSync } from 'node:fs';

const L = { P: '#FF5C93', A: '#F5A524', T: '#2EC4C9', B: '#4C8DFF', V: '#9B6BFF' };
const pts = a => a.map(p => p[0].toFixed(1) + ',' + p[1].toFixed(1)).join(' ');
const poly = (a, f, extra = '') => `<polygon points="${pts(a)}" fill="${f}"${extra}/>`;

// The prism, in the design page's coordinates: a 1024 canvas whose tile spans 100 to 924.
function prism() {
  const T = [[512, 236], [282, 700], [742, 700]];
  const xl = y => T[0][0] - (y - T[0][1]) / (T[1][1] - T[0][1]) * (T[0][0] - T[1][0]);
  const xr = y => T[0][0] + (y - T[0][1]) / (T[2][1] - T[0][1]) * (T[2][0] - T[0][0]);
  const cols = [L.P, L.A, L.T, L.B, L.V];
  const fy0 = 440, fy1 = 560, ey0 = 400, ey1 = 820, n = cols.length;
  let bands = '';
  for (let i = 0; i < n; i++) {
    const a = fy0 + (fy1 - fy0) * i / n, c = fy0 + (fy1 - fy0) * (i + 1) / n, e = ey0 + (ey1 - ey0) * i / n, f = ey0 + (ey1 - ey0) * (i + 1) / n;
    bands += poly([[xr(a), a], [960, e], [960, f], [xr(c), c]], cols[i]);
  }
  // The beam runs past the edge and is masked to outside the prism, so it meets the outline in a clean line instead of
  // its cap and glow poking into the glass.
  const by = 500, inx = xl(by), dx = inx - 100, dy = -90, x2 = inx + dx * 0.1, y2 = by + dy * 0.1;
  const beam = `<g mask="url(#outside)">` +
    `<line x1="40" y1="${by + 90 + 90 * 60 / dx}" x2="${x2}" y2="${y2}" stroke="#FFFFFF" stroke-width="96" stroke-opacity=".16" filter="url(#gw)"/>` +
    `<line x1="40" y1="${by + 90 + 90 * 60 / dx}" x2="${x2}" y2="${y2}" stroke="#FFFFFF" stroke-width="60"/></g>`;
  const wedge = poly([[inx, by], [xr(fy0), fy0], [xr(fy1), fy1]], '#FFFFFF', ' fill-opacity=".22"');
  const mask = `<mask id="outside" maskUnits="userSpaceOnUse" x="0" y="0" width="1024" height="1024">` +
    `<rect width="1024" height="1024" fill="#FFFFFF"/>${poly(T, '#000000')}</mask>`;
  return mask + bands + beam + poly(T, 'url(#pg)') + wedge +
    poly(T, 'none', ' stroke="#FFFFFF" stroke-width="34" stroke-linejoin="round"');
}

// Ink, soft lane-coloured light behind the glass, and the prism scaled so the design's tile fills the canvas.
const k = 1024 / 824;
const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024"><defs>` +
  `<linearGradient id="bg" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#2C2C36"/><stop offset="1" stop-color="#0E0E13"/></linearGradient>` +
  `<linearGradient id="pg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#FFFFFF" stop-opacity=".26"/><stop offset="1" stop-color="#FFFFFF" stop-opacity=".04"/></linearGradient>` +
  `<filter id="bl" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="70"/></filter>` +
  `<filter id="gw" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="22"/></filter></defs>` +
  `<rect width="1024" height="1024" fill="url(#bg)"/>` +
  `<g transform="translate(512 512) scale(${k.toFixed(4)}) translate(-512 -512)">` +
  `<g filter="url(#bl)" opacity=".14"><circle cx="250" cy="260" r="200" fill="${L.P}"/><circle cx="800" cy="300" r="200" fill="${L.B}"/><circle cx="500" cy="860" r="220" fill="${L.A}"/></g>` +
  prism() + `</g></svg>`;

const out = process.argv[2] ?? 'icon.svg';
writeFileSync(out, svg);
console.log(out);
