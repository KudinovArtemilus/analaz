// График признака на странице «Анализ». HTMX вставляет <div data-analysis-chart data-op data-f>,
// этот код загружает ряд из /api/analysis/{op}/feature и рисует:
//   точки — значение признака в каждом рабочем цикле,
//   линия — медиана 51 соседнего цикла (с ней сравниваются аномалии),
//   полоса — норма 5…95% (из базового периода или хранимой нормы), пунктир — медиана нормы,
//   красные точки — циклы, признанные аномальными по этому признаку.
// Цвет — данные (аналитика), красный — только отклонения (как в ISA101).
import uPlot from "./vendor/uPlot.esm.js";

declare const htmx: { onLoad(cb: (el: Element) => void): void };

interface Feature {
  feature: string;
  name: string;
  subsystem: string;
  norm: {
    median: number | null;
    sigma: number | null;
    p5: number | null;
    p95: number | null;
  };
  t: number[];
  v: (number | null)[];
  rm_t?: number[];
  rm?: (number | null)[];
  anom_t: number[];
  anom_v: (number | null)[];
  points: number;
}

const charts = new Map<HTMLElement, { plot: uPlot; ro: ResizeObserver }>();

htmx.onLoad((root) => {
  const boxes = [
    ...root.querySelectorAll<HTMLElement>("[data-analysis-chart]"),
  ];
  if (root instanceof HTMLElement && root.matches("[data-analysis-chart]"))
    boxes.push(root);
  boxes.forEach((box) =>
    render(box).catch((err) => {
      box.innerHTML = `<div class="error">не удалось загрузить график: ${String(err)}</div>`;
    }),
  );
});

// HTMX подменяет график другим признаком — старый уничтожаем, иначе он останется в памяти.
document.addEventListener("htmx:beforeCleanupElement", (e) => {
  const el = e.target as HTMLElement;
  const c = charts.get(el);
  if (c) {
    c.ro.disconnect();
    c.plot.destroy();
    charts.delete(el);
  }
});

async function render(box: HTMLElement): Promise<void> {
  const width = Math.max(box.clientWidth, 320);
  const params = new URLSearchParams({
    f: box.dataset.f ?? "",
    n: String(Math.round(width * 1.5)),
  });
  const res = await fetch(
    `/api/analysis/${encodeURIComponent(box.dataset.op ?? "")}/feature?${params}`,
  );
  if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
  const d = (await res.json()) as Feature;

  // три ряда со своими временами сводим в одну таблицу (uPlot требует общий x)
  const tables: uPlot.AlignedData[] = [[d.t, d.v] as uPlot.AlignedData];
  if (d.rm_t && d.rm) tables.push([d.rm_t, d.rm] as uPlot.AlignedData);
  tables.push([d.anom_t, d.anom_v] as uPlot.AlignedData);
  const data = uPlot.join(tables);

  const c = palette();
  const series: uPlot.Series[] = [
    { label: "время", value: (_u, v) => (v == null ? "—" : fmtTime(v)) },
    {
      label: "цикл",
      stroke: c.data,
      width: 0,
      paths: () => null,
      points: { show: true, size: 4, fill: c.data, stroke: c.data },
      value: val,
    },
  ];
  if (d.rm_t)
    series.push({
      label: "медиана соседних",
      stroke: c.line,
      width: 2,
      spanGaps: true,
      points: { show: false },
      value: val,
    });
  series.push({
    label: "аномалия",
    stroke: c.danger,
    width: 0,
    paths: () => null,
    points: { show: true, size: 7, fill: c.danger, stroke: c.danger },
    value: val,
  });

  const n = d.norm;
  const axis: uPlot.Axis = {
    stroke: c.muted,
    grid: { stroke: c.grid, width: 1 },
    ticks: { stroke: c.grid, width: 1 },
    font: "11px system-ui, sans-serif",
  };
  const opts: uPlot.Options = {
    width,
    height: 320,
    title: `${d.name}${d.points > d.t.length ? ` · ${d.points} циклов, показано ${d.t.length}` : ""}`,
    // время в таблице — по часам пресса без пояса, поэтому показываем «как есть» (UTC)
    tzDate: (ts) => uPlot.tzDate(new Date(ts * 1e3), "Etc/UTC"),
    cursor: { drag: { x: true, y: false } },
    scales: {
      y: {
        range: (_u, lo, hi) => {
          // полоса нормы должна быть видна, даже если сейчас признак далеко от неё
          const vals = [lo, hi, n.p5, n.p95].filter(
            (x): x is number => x != null && Number.isFinite(x),
          );
          const a = Math.min(...vals),
            b = Math.max(...vals),
            pad = (b - a) * 0.06 || 1;
          return [a - pad, b + pad];
        },
      },
    },
    axes: [
      // подписи дат по-русски: 13.11 (или 13.11 08:00 на коротком отрезке), а не американские 11/13
      {
        ...axis,
        values: (u, splits) =>
          splits.map((t) =>
            fmtTick(t, (u.scales.x.max ?? 0) - (u.scales.x.min ?? 0)),
          ),
      },
      { ...axis, size: 60 },
    ],
    series,
    hooks: {
      // полосу и медиану нормы рисуем под данными
      drawAxes: [
        (u) => {
          const ctx = u.ctx,
            { left, top, width: w, height: h } = u.bbox;
          ctx.save();
          ctx.beginPath();
          ctx.rect(left, top, w, h);
          ctx.clip();
          if (n.p5 != null && n.p95 != null) {
            const y1 = u.valToPos(n.p95, "y", true),
              y2 = u.valToPos(n.p5, "y", true);
            ctx.fillStyle = c.band;
            ctx.fillRect(left, y1, w, Math.max(y2 - y1, 1));
          }
          if (n.median != null) {
            const y = u.valToPos(n.median, "y", true);
            ctx.strokeStyle = c.muted;
            ctx.setLineDash([6, 4]);
            ctx.lineWidth = 1;
            ctx.beginPath();
            ctx.moveTo(left, y);
            ctx.lineTo(left + w, y);
            ctx.stroke();
          }
          ctx.restore();
        },
      ],
    },
  };

  box.innerHTML = "";
  const plot = new uPlot(opts, data, box);
  const ro = new ResizeObserver(() =>
    plot.setSize({ width: Math.max(box.clientWidth, 320), height: 320 }),
  );
  ro.observe(box);
  charts.set(box, { plot, ro });
}

function val(_u: uPlot, v: number | null): string {
  if (v == null) return "—";
  const a = Math.abs(v);
  return a >= 100
    ? v.toFixed(0)
    : a >= 10
      ? v.toFixed(1)
      : a >= 0.1
        ? v.toFixed(2)
        : v.toFixed(3);
}

function fmtTick(ts: number, spanSec: number): string {
  const d = new Date(ts * 1e3);
  const p = (x: number) => String(x).padStart(2, "0");
  const day = `${p(d.getUTCDate())}.${p(d.getUTCMonth() + 1)}`;
  return spanSec > 3 * 86400
    ? day
    : `${day} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())}`;
}

function fmtTime(ts: number): string {
  const d = new Date(ts * 1e3);
  const p = (x: number) => String(x).padStart(2, "0");
  return `${p(d.getUTCDate())}.${p(d.getUTCMonth() + 1)} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())}`;
}

interface Colors {
  data: string;
  line: string;
  band: string;
  danger: string;
  muted: string;
  grid: string;
}

function palette(): Colors {
  const css = getComputedStyle(document.documentElement);
  const v = (name: string) => css.getPropertyValue(name).trim();
  return {
    data: v("--muted"),
    line: v("--line-1"),
    band: "rgba(127,127,127,0.16)",
    danger: v("--danger"),
    muted: v("--muted"),
    grid: v("--grid"),
  };
}
