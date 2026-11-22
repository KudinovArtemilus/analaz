// 6.2д — «Как ведут себя признаки перед остановами» (charts.py, раздел 6).
//
// 12 самых долгих простоев совмещены по моменту останова: 0 — последний цикл перед паузой,
// −24 — сутки до него. Четыре признака, которые чаще всего отклонялись перед остановами,
// в σ от нормы (медиана по 5 циклам). Серая полоса — норма ±3σ. Если линии систематически
// уходят из полосы за N часов до 0 — это запас времени на предупреждение.
// Щелчок по останову в легенде прячет его линии сразу на всех графиках. Данные —
// /api/analysis/{op}/prestops, расчёт сверен с Python.
import { init, type ECharts } from "./vendor/echarts.esm.js";

declare const htmx: { onLoad(cb: (el: Element) => void): void };

type N = number | null;
interface PreStops {
    features: { feature: string; name: string }[];
    lines: { label: string; color: number; x: N[]; z: N[][] }[];
}

const owned = new Map<HTMLElement, { chart: ECharts; ro: ResizeObserver }>();

htmx.onLoad((root) => {
    const boxes = [...root.querySelectorAll<HTMLElement>("[data-prestops]")];
    if (root instanceof HTMLElement && root.matches("[data-prestops]")) boxes.push(root);
    boxes.forEach((box) =>
        render(box).catch((err) => {
            box.innerHTML = `<div class="error">не удалось загрузить: ${String(err)}</div>`;
        }),
    );
});

document.addEventListener("htmx:beforeCleanupElement", (e) => {
    const o = owned.get(e.target as HTMLElement);
    if (o) {
        o.ro.disconnect();
        o.chart.dispose();
        owned.delete(e.target as HTMLElement);
    }
});

async function render(box: HTMLElement): Promise<void> {
    const op = box.dataset.op ?? "";
    const res = await fetch(`/api/analysis/${encodeURIComponent(op)}/prestops`);
    if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
    const d = (await res.json()) as PreStops;
    if (!d.features.length || !d.lines.length) {
        box.innerHTML = `<div class="muted small">остановов с отклонениями перед ними нет</div>`;
        return;
    }
    const c = palette();
    const n = d.features.length;
    const H = 200, top = 34, gap = 46, legendH = 56; // высота одного графика, отступы
    const el = document.createElement("div");
    el.style.height = `${top + n * H + (n - 1) * gap + legendH + 30}px`;
    box.innerHTML = "";
    box.appendChild(el);

    const axis = { axisLabel: { color: c.muted }, axisLine: { lineStyle: { color: c.grid } }, splitLine: { lineStyle: { color: c.grid } } };
    const grids = d.features.map((_f, j) => ({ left: 56, right: 20, top: top + j * (H + gap), height: H }));
    const series: object[] = [];
    d.lines.forEach((l) => {
        const color = c.cats[l.color % c.cats.length];
        d.features.forEach((_f, j) => {
            series.push({
                name: l.label, type: "line", xAxisIndex: j, yAxisIndex: j,
                data: l.x.map((x, i) => [x, l.z[j][i]]),
                symbol: "none", lineStyle: { width: 1.3, color }, itemStyle: { color },
                connectNulls: false,
                // полоса нормы — один раз на каждом графике
                ...(l === d.lines[0] ? { markArea: { silent: true, itemStyle: { color: c.band }, data: [[{ yAxis: -3 }, { yAxis: 3 }]] } } : {}),
            });
        });
    });
    const chart = init(el);
    chart.setOption({
        textStyle: { color: c.text, fontFamily: "system-ui, sans-serif", fontSize: 11 },
        animation: false,
        // заголовок над каждым графиком — название признака
        title: d.features.map((f, j) => ({
            text: f.name, left: 56, top: top + j * (H + gap) - 22,
            textStyle: { color: c.text, fontSize: 12, fontWeight: 500 },
        })),
        legend: { type: "scroll", bottom: 0, textStyle: { color: c.text }, pageTextStyle: { color: c.muted } },
        tooltip: {
            trigger: "item",
            formatter: (p: any) => `${p.seriesName}<br>${(-p.value[0]).toFixed(1)} ч до останова<br>${d.features[p.seriesIndex % n].name}: ${p.value[1] == null ? "—" : (+p.value[1]).toFixed(1)}σ`,
        },
        // курсор общий для всех графиков — один и тот же момент «до останова»
        axisPointer: { link: [{ xAxisIndex: "all" }], label: { backgroundColor: c.muted } },
        // колесо мыши / тачпад — приблизить последние часы перед остановом, на всех графиках сразу
        dataZoom: [{ type: "inside", xAxisIndex: d.features.map((_f, j) => j), filterMode: "none" }],
        grid: grids,
        xAxis: d.features.map((_f, j) => ({
            type: "value", gridIndex: j, min: -72, max: 0, interval: 12, ...axis,
            axisPointer: { show: true, type: "line", lineStyle: { color: c.muted } },
            ...(j === n - 1 ? { name: "часы до останова", nameLocation: "middle", nameGap: 26, nameTextStyle: { color: c.muted } } : {}),
        })),
        yAxis: d.features.map((_f, j) => ({ type: "value", gridIndex: j, scale: true, name: "σ", nameTextStyle: { color: c.muted }, ...axis })),
        series,
    });
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(box);
    owned.set(box, { chart, ro });
}

function palette() {
    const css = getComputedStyle(document.documentElement);
    const v = (n: string) => css.getPropertyValue(n).trim();
    const dark = matchMedia("(prefers-color-scheme: dark)").matches;
    return {
        text: v("--text"),
        muted: v("--muted"),
        grid: v("--grid"),
        band: dark ? "rgba(255,255,255,0.07)" : "rgba(0,0,0,0.06)",
        // 12 остановов — 12 различимых цветов (та же палитра, что у подсистем и групп)
        cats: ["#378add", "#d85a30", "#1d9e75", "#7f77dd", "#ba7517", "#d4537e", "#5f5e5a", "#639922", "#0f6e56", "#993c1d", "#534ab7", "#888780"],
    };
}