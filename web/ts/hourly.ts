// 6.2з — «Сутки по часам» и «Циклов в час» в отчёте за сутки (daily.py write_html).
// Данные лежат во фрагменте отчёта (<script type="application/json">), расчёт сверен с Python.
// Время — часы от начала суток (0…24), подписи — ЧЧ:ММ.
import { init, type ECharts } from "./vendor/echarts.esm.js";

declare const htmx: { onLoad(cb: (el: Element) => void): void };

type N = number | null;
interface Hourly {
    plots: { name: string; x: N[]; v: N[]; hx: N[]; hm: N[]; lo: N; hi: N }[];
    per_hour: [number, number][];
}

const owned = new Map<HTMLElement, { charts: ECharts[]; ro: ResizeObserver }>();

htmx.onLoad((root) => {
    const boxes = [...root.querySelectorAll<HTMLElement>("[data-day-hourly]")];
    if (root instanceof HTMLElement && root.matches("[data-day-hourly]")) boxes.push(root);
    boxes.forEach((box) => {
        try {
            render(box);
        } catch (err) {
            box.innerHTML = `<div class="error">не удалось нарисовать: ${String(err)}</div>`;
        }
    });
});

document.addEventListener("htmx:beforeCleanupElement", (e) => {
    const o = owned.get(e.target as HTMLElement);
    if (o) {
        o.ro.disconnect();
        o.charts.forEach((c) => c.dispose());
        owned.delete(e.target as HTMLElement);
    }
});

const hhmm = (h: number) => {
    const m = Math.round(h * 60);
    return `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
};

function render(box: HTMLElement): void {
    const src = box.querySelector("script[type='application/json']");
    if (!src) return;
    const d = JSON.parse(src.textContent ?? "{}") as Hourly;
    const c = palette();
    const charts: ECharts[] = [];
    const grid = document.createElement("div");
    grid.className = "hr-grid";
    box.replaceChildren(grid);

    const common = { textStyle: { color: c.text, fontFamily: "system-ui, sans-serif", fontSize: 11 }, animation: false };
    const axis = { axisLabel: { color: c.muted }, axisLine: { lineStyle: { color: c.grid } }, splitLine: { lineStyle: { color: c.grid } } };
    // ось времени: от первого до последнего часа суток с данными, подписи ЧЧ:ММ
    const xAxis = (from: number, to: number) => ({
        type: "value", min: Math.floor(from), max: Math.ceil(to), interval: Math.max(1, Math.ceil((to - from) / 6)),
        ...axis, axisLabel: { color: c.muted, formatter: hhmm },
    });
    const cell = (title: string) => {
        const el = document.createElement("div");
        el.className = "hr-cell";
        el.innerHTML = `<div class="hr-title" title="${title.replace(/"/g, "&quot;")}">${title}</div>`;
        const ch = document.createElement("div");
        ch.className = "hr-chart";
        el.appendChild(ch);
        grid.appendChild(el);
        const chart = init(ch);
        charts.push(chart);
        return chart;
    };

    for (const p of d.plots) {
        const xs = p.x.filter((x): x is number => x != null);
        cell(p.name).setOption({
            ...common,
            grid: { left: 48, right: 8, top: 6, bottom: 22 },
            tooltip: { trigger: "item", formatter: (q: any) => `${hhmm(q.value[0])}<br>${fmt(q.value[1])}${q.seriesIndex ? " (медиана за час)" : ""}` },
            xAxis: xAxis(Math.min(...xs), Math.max(...xs)),
            yAxis: { type: "value", scale: true, ...axis },
            series: [
                {
                    type: "scatter", symbolSize: 3, data: p.x.map((x, i) => [x, p.v[i]]),
                    itemStyle: { color: c.point, opacity: 0.7 },
                    // полоса нормы 5…95% — серая, как везде: это фон, а не тревога
                    ...(p.lo != null && p.hi != null
                        ? { markArea: { silent: true, itemStyle: { color: c.band }, data: [[{ yAxis: p.lo }, { yAxis: p.hi }]] } }
                        : {}),
                },
                { type: "line", data: p.hx.map((x, i) => [x, p.hm[i]]), symbol: "circle", symbolSize: 4, lineStyle: { width: 2, color: c.line }, itemStyle: { color: c.line } },
            ],
        });
    }

    if (d.per_hour.length) {
        const hs = d.per_hour.map(([h]) => h);
        cell("Циклов в час").setOption({
            ...common,
            grid: { left: 48, right: 8, top: 6, bottom: 22 },
            tooltip: { trigger: "item", formatter: (q: any) => `${hhmm(q.value[0])}–${hhmm(q.value[0] + 1)}: ${q.value[1]} циклов` },
            xAxis: xAxis(Math.min(...hs), Math.max(...hs) + 1),
            yAxis: { type: "value", minInterval: 1, ...axis },
            // столбик — на середине своего часа
            series: [{ type: "bar", data: d.per_hour.map(([h, n]) => [h + 0.5, n]), barWidth: "70%", itemStyle: { color: c.point } }],
        });
    }

    const ro = new ResizeObserver(() => charts.forEach((ch) => ch.resize()));
    ro.observe(box);
    owned.set(box, { charts, ro });
}

function palette() {
    const css = getComputedStyle(document.documentElement);
    const v = (n: string) => css.getPropertyValue(n).trim();
    const dark = matchMedia("(prefers-color-scheme: dark)").matches;
    return {
        text: v("--text"),
        muted: v("--muted"),
        grid: v("--grid"),
        band: dark ? "rgba(255,255,255,0.08)" : "rgba(0,0,0,0.07)",
        point: "#378add",
        line: dark ? "#e8e6e0" : "#2c2c2a", // медиана за час — тёмная (в тёмной теме светлая) линия поверх точек
    };
}

function fmt(v: N): string {
    return v == null ? "—" : String(Number((+v).toPrecision(5)));
}