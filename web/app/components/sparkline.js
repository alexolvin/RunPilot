// Спарклайн (SVG, 11.9): значения → ломаная. Тон — токен цвета (var(--…));
// размеры viewBox — constants.js (R2). Пусто/мало точек → плейсхолдер.
import { html } from '../html.js';
import { CHART } from '../constants.js';

export function Sparkline(props) {
  const values = (props.values || []).map(Number).filter((n) => !Number.isNaN(n));
  const tone = props.tone || 'var(--color-accent)';
  if (values.length < CHART.minPoints) {
    return html`<div class="spark spark--empty"></div>`;
  }
  const w = CHART.w;
  const h = CHART.h;
  let max = -1;
  let min = 1;
  for (const v of values) {
    if (v > max) max = v;
    if (v < min) min = v;
  }
  if (max < 1) max = 1;
  if (min > 0) min = 0;
  const range = max - min;
  const step = w / (values.length - 1);
  const pts = values
    .map((v, i) => {
      const x = i * step;
      const y = range > 0 ? h - ((v - min) / range) * h : h * CHART.mid;
      return x.toFixed(1) + ',' + y.toFixed(1);
    })
    .join(' ');
  return html`<svg class="spark" viewBox="0 0 ${w} ${h}" preserveAspectRatio="none"
    aria-hidden="true">
    <polyline class="spark-line" points=${pts} fill="none" stroke=${tone} />
  </svg>`;
}
