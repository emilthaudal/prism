// Writes the art of Prism's app icon, "The split", as three full-bleed SVG layers for the macOS 26 .icon
// (Resources/Prism.icon): a glass prism on ink; one white beam enters from the left and leaves as the five lane colours.
// The drawing is pSplit from docs/design/prism-identity.html, where Emil picked it (2026-11-25), with two changes:
// - full bleed, no tile, sheen or edge, since macOS masks the square and adds its own glass;
// - a thicker outline (34) and beam (60), and the outline drawn last over the glass and the light wedge: the macOS 26
//   .icon format draws every size from this one picture (no per-size artwork), so following Apple's guidance the one
//   design carries few, bold shapes that still read at 16 px: darker glass and fainter background glows, so the white
//   outline and beam keep their contrast when the whole icon is 16 pixels.
// The layers, back to front; the ink gradient behind them is the fill in icon.json, not a picture:
// - glows.svg: soft lane-coloured light on the ink, flat;
// - light.svg: the beam and the five bands, flat, so the lane colours stay exactly the lane colours;
// - prism.svg: the glass, the light wedge inside it and the outline. The only layer macOS treats as glass, so the
//   prism alone gets the system's edge highlight and shadow.
// Usage: node scripts/icon.mjs OUTDIR
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const L = { P: '#FF5C93', A: '#F5A524', T: '#2EC4C9', B: '#4C8DFF', V: '#9B6BFF' };
const pts = a => a.map(p => p[0].toFixed(1) + ',' + p[1].toFixed(1)).join(' ');
const poly = (a, f, extra = '') => `<polygon points="${pts(a)}" fill="${f}"${extra}/>`;

// The prism, in the design page's coordinates: a 1024 canvas whose tile spans 100 to 924. Every layer is scaled so
// that tile fills the canvas.
const T = [[512, 236], [282, 700], [742, 700]];
const xl = y => T[0][0] - (y - T[0][1]) / (T[1][1] - T[0][1]) * (T[0][0] - T[1][0]);
const xr = y => T[0][0] + (y - T[0][1]) / (T[2][1] - T[0][1]) * (T[2][0] - T[0][0]);
const by = 500, fy0 = 440, fy1 = 560; // where the beam enters the left face, and the span it leaves the right face by
const k = 1024 / 824;
const layer = (defs, body) =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024"><defs>${defs}</defs>` +
  `<g transform="translate(512 512) scale(${k.toFixed(4)}) translate(-512 -512)">${body}</g></svg>`;
// Blur filters and the mask cover the whole canvas, and a blurred shape sits in a group with `pad`, an invisible
// rectangle of that size: WebKit cuts a blur off at the box of what it blurs, whatever region the filter names, which
// cut the beam's glow off in a straight line above and below it.
const all = 'x="-300" y="-300" width="1624" height="1624"';
const pad = `<rect ${all} fill="#FFFFFF" fill-opacity="0"/>`;
const blur = (id, sd) => `<filter id="${id}" filterUnits="userSpaceOnUse" ${all}><feGaussianBlur stdDeviation="${sd}"/></filter>`;

function glows() {
  return layer(blur('bl', 70), `<g filter="url(#bl)" opacity=".14"><circle cx="250" cy="260" r="200" fill="${L.P}"/>` +
    `<circle cx="800" cy="300" r="200" fill="${L.B}"/><circle cx="500" cy="860" r="220" fill="${L.A}"/></g>`);
}

function light() {
  const cols = [L.P, L.A, L.T, L.B, L.V], ey0 = 400, ey1 = 820, n = cols.length;
  // Each band runs from its own top edge to the bottom of the fan and the next is drawn over it. Bands that only
  // touch leave a dark hairline between them, where both edges are antialiased against the ink.
  let bands = '';
  for (let i = 0; i < n; i++) {
    const a = fy0 + (fy1 - fy0) * i / n, e = ey0 + (ey1 - ey0) * i / n;
    bands += poly([[xr(a), a], [960, e], [960, ey1], [xr(fy1), fy1]], cols[i]);
  }
  // The beam runs past the edge and is masked to outside the prism, so it meets the outline in a clean line instead of
  // its cap and glow poking into the glass.
  const inx = xl(by), dx = inx - 100, dy = -90, x2 = inx + dx * 0.1, y2 = by + dy * 0.1, y1 = by + 90 + 90 * 60 / dx;
  const mask = `<mask id="outside" maskUnits="userSpaceOnUse" ${all}><rect ${all} fill="#FFFFFF"/>${poly(T, '#000000')}</mask>`;
  const beam = `<g mask="url(#outside)">` +
    `<g filter="url(#gw)">${pad}<line x1="40" y1="${y1}" x2="${x2}" y2="${y2}" stroke="#FFFFFF" stroke-width="96" stroke-opacity=".16"/></g>` +
    `<line x1="40" y1="${y1}" x2="${x2}" y2="${y2}" stroke="#FFFFFF" stroke-width="60"/></g>`;
  return layer(mask + blur('gw', 22), bands + beam);
}

function prism() {
  const glass = `<linearGradient id="pg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#FFFFFF" stop-opacity=".26"/>` +
    `<stop offset="1" stop-color="#FFFFFF" stop-opacity=".04"/></linearGradient>`;
  const wedge = poly([[xl(by), by], [xr(fy0), fy0], [xr(fy1), fy1]], '#FFFFFF', ' fill-opacity=".22"');
  return layer(glass, poly(T, 'url(#pg)') + wedge + poly(T, 'none', ' stroke="#FFFFFF" stroke-width="34" stroke-linejoin="round"'));
}

const out = process.argv[2] ?? '.';
mkdirSync(out, { recursive: true });
for (const [name, svg] of Object.entries({ glows: glows(), light: light(), prism: prism() })) {
  writeFileSync(join(out, name + '.svg'), svg);
  console.log(join(out, name + '.svg'));
}
