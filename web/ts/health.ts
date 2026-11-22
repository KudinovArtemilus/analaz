// Карта здоровья подсистем (6.2б): тепловая карта «подсистема × день», под ней — общий индекс
// нездоровья пресса и аномальные циклы по подсистемам. Данные — /api/analysis/{op}/health.
// Щелчок по клетке подгружает (HTMX) список признаков этой подсистемы за этот день.
//
// Это режим аналитики, поэтому цвет — шкала данных (серый → янтарный → красный), а не тревога.
// Зелёного нет и здесь: «в норме» — нейтральный цвет фона, как в ISA101.
import { init, type ECharts } from "./vendor/echarts.esm.js";

declare const htmx: {
    onLoad(cb: (el: Element) => void): void;
    ajax(method: string, url: string, opts: { target: string; swap?: string }): void;
};

interface Health {
    step: "day" | "hour";
    subsystems: string[];
    bins: string[];
    z: (number | null)[][];
    index: (number | null)[];
    anom_bins: string[];
    anom: number[][];
    zmax: number;
}

const charts = new Map<HTMLElement, { list: ECharts[]; ro: ResizeObserver }>();

htmx.onLoad((root) => {
    const boxes = [...root.querySelectorAll<HTMLElement>("[data-health]")];
    if (root instanceof HTMLElement && root.matches("[data-health]")) boxes.push(root);
    boxes.forEach((box) =>
        render(box).catch((err) => {
            box.innerHTML = `<div class="error">не удалось загрузить карту здоровья: ${String(err)}</div>`;
        }),
    );
});

document.addEventListener("htmx:beforeCleanupElement", (e) => {
    const c = charts.get(e.target as HTMLElement);
    if (c) {
        c.ro.disconnect();
        c.list.forEach((x) => x.dispose());
        charts.delete(e.target as HTMLElement);
    }
});

async function render(box: HTMLElement): Promise<void> {
    const op = box.dataset.op ?? "";
    const res = await fetch(`/api/analysis/${encodeURIComponent(op)}/health`);
    if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
    const h = (await res.json()) as Health;
    if (!h.subsystems.length || !h.bins.length) {
        box.innerHTML = `<div class="muted small">недостаточно данных для карты</div>`;
        return;
    }
    const c = palette();
    const unit = h.step === "day" ? "день" : "час";
    const label = (s: string) => (h.step === "day" ? `${s.slice(8, 10)}.${s.slice(5, 7)}` : `${s.slice(8, 10)}.${s.slice(5, 7)} ${s.slice(11, 16)}`);

    box.innerHTML = `<div class="hm"></div><div class="hm-index"></div><div class="hm-anom"></div>`;
    const [elMap, elIdx, elAnom] = [...box.children] as HTMLElement[];
    elMap.style.height = `${80 + 26 * h.subsystems.length}px`;
    elIdx.style.height = "180px";
    elAnom.style.height = "220px";
    const common = {
        textStyle: { color: c.text, fontFamily: "system-ui, sans-serif", fontSize: 11 },
        animation: false,
    };
    const axisX = (data: string[]) => ({
        type: "category" as const,
        data: data.map(label),
        axisLabel: { color: c.muted },
        axisLine: { lineStyle: { color: c.grid } },
    });

    // 1. Тепловая карта
    const cells: (number | null)[][] = [];
    h.z.forEach((row, k) => row.forEach((v, b) => cells.push([b, k, v == null ? null : Math.round(v * 10) / 10])));
    const map = init(elMap);
    map.setOption({
        ...common,
        grid: { left: 210, right: 70, top: 8, bottom: 30 },
        tooltip: {
            formatter: (p: any) =>
                p.value[2] == null
                    ? `${h.subsystems[p.value[1]]}<br>${label(h.bins[p.value[0]])}: нет данных`
                    : `${h.subsystems[p.value[1]]}<br>${label(h.bins[p.value[0]])}: отклонение ${p.value[2]}σ<br><span style="opacity:.7">щелчок — признаки</span>`,
        },
        xAxis: { ...axisX(h.bins), splitArea: { show: false } },
        yAxis: { type: "category", data: h.subsystems, inverse: true, axisLabel: { color: c.text }, axisLine: { show: false }, axisTick: { show: false } },
        visualMap: {
            min: 0, max: h.zmax, calculable: false, orient: "vertical", right: 0, top: 8, itemHeight: 120,
            text: [`${Math.round(h.zmax)}σ`, "0"], textStyle: { color: c.muted },
            inRange: { color: c.scale },
        },
        series: [{ type: "heatmap", data: cells, itemStyle: { borderColor: c.bg, borderWidth: 1 }, emphasis: { itemStyle: { borderColor: c.text } } }],
    });
    map.on("click", (p: any) => {
        const [b, k] = p.value as [number, number];
        htmx.ajax("GET", `/analysis/health/cell?op=${encodeURIComponent(op)}&sub=${encodeURIComponent(h.subsystems[k])}&b=${b}`, { target: "#hm-cell" });
    });

    // 2. Общий индекс нездоровья: худшая подсистема за интервал, полосы 3σ и 6σ
    const idx = init(elIdx);
    idx.setOption({
        ...common,
        title: { text: `Общий индекс нездоровья пресса (худшая подсистема за ${unit}), σ`, textStyle: { color: c.text, fontSize: 12, fontWeight: 500 }, left: 0 },
        grid: { left: 50, right: 20, top: 42, bottom: 28 },
        tooltip: { trigger: "axis", valueFormatter: (v: any) => (v == null ? "—" : `${(+v).toFixed(1)}σ`) },
        xAxis: axisX(h.bins),
        yAxis: { type: "value", axisLabel: { color: c.muted }, splitLine: { lineStyle: { color: c.grid } } },
        series: [{
            type: "line", data: h.index, connectNulls: true, symbolSize: 5,
            lineStyle: { color: c.text, width: 1.5 }, itemStyle: { color: c.text },
            markArea: {
                silent: true, data: [
                    [{ yAxis: 3, itemStyle: { color: "rgba(239,159,39,0.12)" } }, { yAxis: 6 }],
                    [{ yAxis: 6, itemStyle: { color: "rgba(163,45,45,0.10)" } }, { yAxis: Math.max(h.zmax, 6) * 10 }],
                ]
            },
        }],
    });

    // 3. Аномальные циклы по подсистемам (столбики с накоплением; пустые подсистемы не показываем)
    const anom = init(elAnom);
    const series = h.subsystems
        .map((name, k) => ({ name, k, total: h.anom[k].reduce((a, b) => a + b, 0) }))
        .filter((x) => x.total > 0)
        // цвет привязан к подсистеме, а не к порядку столбиков: у OP1 и OP2 одна подсистема — один цвет
        .map((x) => ({ name: x.name, type: "bar", stack: "a", data: h.anom[x.k], itemStyle: { color: subColor(x.name, c.cats) }, emphasis: { focus: "series" } }));
    anom.setOption({
        ...common,
        title: { text: `Аномальные циклы по подсистемам за ${unit}`, textStyle: { color: c.text, fontSize: 12, fontWeight: 500 }, left: 0 },
        grid: { left: 50, right: 20, top: 42, bottom: 56 },
        legend: { bottom: 0, type: "scroll", textStyle: { color: c.muted } },
        tooltip: { trigger: "axis", axisPointer: { type: "shadow" } },
        xAxis: axisX(h.anom_bins),
        yAxis: { type: "value", minInterval: 1, axisLabel: { color: c.muted }, splitLine: { lineStyle: { color: c.grid } } },
        series,
    });

    const list = [map, idx, anom];
    const ro = new ResizeObserver(() => list.forEach((x) => x.resize()));
    ro.observe(box);
    charts.set(box, { list, ro });
}

export function palette() {
    const css = getComputedStyle(document.documentElement);
    const v = (n: string) => css.getPropertyValue(n).trim();
    const dark = matchMedia("(prefers-color-scheme: dark)").matches;
    const cell0 = v("--hover") || "#eeeeee";
    return {
        // шкала от «фона» к красному; в тёмной теме промежуточные цвета темнее, иначе слабые
        // отклонения светятся ярче сильных
        scale: dark ? [cell0, "#4a3d1f", "#8a5a12", "#c8611f", "#e24b4a"] : [cell0, "#efe3bd", "#ef9f27", "#d85a30", "#a32d2d"],
        text: v("--text"),
        muted: v("--muted"),
        grid: v("--grid"),
        bg: v("--panel") || v("--bg"),
        // категориальная палитра для подсистем: приглушённые, различимые и в тёмной теме
        cats: ["#378add", "#d85a30", "#1d9e75", "#7f77dd", "#ba7517", "#d4537e", "#5f5e5a", "#639922", "#0f6e56", "#993c1d", "#534ab7", "#888780"],
    };
}

// Постоянный цвет подсистемы на всех страницах и операциях. Порядок — как у подсистем в анализе.
const SUBS = ["Цилиндры CCyl/LCyl", "Подвижный стол (MB)", "Аккумуляторы", "Выталкиватели", "Насосы (утечки)", "Цикл",
    "Зажим штампа", "Привод насосов", "Ползун", "Гидростанция", "Насосы (регулирование)", "Сервоклапаны"];

function subColor(name: string, cats: string[]): string {
    let i = SUBS.indexOf(name);
    if (i < 0) i = [...name].reduce((a, ch) => (a * 31 + ch.charCodeAt(0)) >>> 0, 7) % cats.length; // незнакомая — по хешу имени
    return cats[i % cats.length];
}