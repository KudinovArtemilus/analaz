// 6.2г — «Было / стало», «Что меняется вместе», «Связи и производительность»
// (charts.py, разделы 4, 5, 7). Данные — /api/analysis/{op}/compare, расчёт сверен с Python.
//
//   • было / стало — распределение признака в норме и в последних 20% циклов на общей сетке
//     столбиков: сдвиг или расширение «горба» = поведение узла изменилось;
//   • что меняется вместе — корреляция Спирмена между дневными медианами 25 самых «трендовых»
//     признаков: красный блок — растут вместе, часто одна первопричина;
//   • связи — облака «признак против признака», цвет — время: облако «уезжает» с цветом =
//     при тех же условиях узел ведёт себя иначе;
//   • длительность цикла по дням — «ящики с усами».
import { init, type ECharts } from "./vendor/echarts.esm.js";

declare const htmx: { onLoad(cb: (el: Element) => void): void };

type N = number | null;
interface Hist {
    feature: string; name: string;
    edges: N[]; base: N[]; tail: N[];
    base_n: number; tail_n: number; base_med: N; tail_med: N; clipped: number;
}
interface Compare {
    before_after: Hist[];
    corr: { features: string[]; names: string[]; full: string[]; rho: N[][] } | null;
    pairs: { title: string; x_name: string; y_name: string; x: N[]; y: N[]; days: N[] }[];
    step: "day" | "hour";
    cycle: { start: string; n: number; box: N[] }[];
}

const owned = new Map<HTMLElement, { charts: ECharts[]; ro: ResizeObserver }>();

htmx.onLoad((root) => {
    const boxes = [...root.querySelectorAll<HTMLElement>("[data-compare]")];
    if (root instanceof HTMLElement && root.matches("[data-compare]")) boxes.push(root);
    boxes.forEach((box) =>
        render(box).catch((err) => {
            box.innerHTML = `<div class="error">не удалось загрузить: ${esc(String(err))}</div>`;
        }),
    );
});

document.addEventListener("htmx:beforeCleanupElement", (e) => {
    const o = owned.get(e.target as HTMLElement);
    if (o) {
        o.ro.disconnect();
        o.charts.forEach((c) => c.dispose());
        owned.delete(e.target as HTMLElement);
    }
});

async function render(box: HTMLElement): Promise<void> {
    const op = box.dataset.op ?? "";
    const res = await fetch(`/api/analysis/${encodeURIComponent(op)}/compare`);
    if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
    const d = (await res.json()) as Compare;
    const c = palette();
    const charts: ECharts[] = [];
    box.innerHTML = "";

    // div для одного графика нужной высоты
    const place = (parent: HTMLElement, height: number, cls = "cmp-chart") => {
        const el = document.createElement("div");
        el.className = cls;
        el.style.height = `${height}px`;
        parent.appendChild(el);
        const ch = init(el);
        charts.push(ch);
        return ch;
    };
    const section = (title: string, help: string) => {
        const s = document.createElement("div");
        s.className = "cmp-sec";
        s.innerHTML = `<h4>${title}</h4><p class="muted small">${help}</p>`;
        box.appendChild(s);
        return s;
    };
    const common = { textStyle: { color: c.text, fontFamily: "system-ui, sans-serif", fontSize: 11 }, animation: false };
    const axis = { axisLabel: { color: c.muted }, axisLine: { lineStyle: { color: c.grid } }, splitLine: { lineStyle: { color: c.grid } } };

    // ---------- было / стало ----------
    if (d.before_after.length) {
        const s = section("Было / стало",
            "Распределение признаков с самым сильным трендом: серым — базовый период (норма), цветом — последние 20% циклов. " +
            "Сдвиг или расширение «горба» = изменение поведения. Высота столбика — доля циклов набора.");
        const grid = document.createElement("div");
        grid.className = "cmp-grid";
        s.appendChild(grid);
        for (const h of d.before_after) {
            const cell = document.createElement("div");
            cell.className = "cmp-cell";
            const t = document.createElement("div");
            t.className = "cmp-title";
            t.title = h.name; // полное имя — во всплывающей подсказке
            t.innerHTML = `<span class="cmp-name">${esc(h.name)}</span>` +
                `<span class="muted"> было ${fmt(h.base_med)} → стало <b>${fmt(h.tail_med)}</b></span>`;
            cell.appendChild(t);
            grid.appendChild(cell);
            const mids = h.edges.slice(0, -1).map((e, k) => ((e ?? 0) + (h.edges[k + 1] ?? 0)) / 2);
            place(cell, 170).setOption({
                ...common,
                grid: { left: 36, right: 8, top: 8, bottom: 22 },
                tooltip: {
                    trigger: "axis",
                    formatter: (ps: any[]) => {
                        const k = ps[0].dataIndex;
                        return `${fmt(h.edges[k])} … ${fmt(h.edges[k + 1])}<br>` +
                            ps.map((p) => `${p.marker}${p.seriesName}: ${(p.value * 100).toFixed(1)}%`).join("<br>");
                    },
                },
                xAxis: { type: "category", data: mids.map(fmt), ...axis, axisLabel: { color: c.muted, interval: Math.ceil(mids.length / 4) - 1 } },
                yAxis: { type: "value", ...axis, axisLabel: { color: c.muted, formatter: (v: number) => `${Math.round(v * 100)}%` } },
                series: [
                    // общая сетка и «наложение»: barGap -100% ставит столбики друг на друга, полупрозрачно
                    { name: `норма (${h.base_n})`, type: "bar", data: h.base, barGap: "-100%", barCategoryGap: "0%", itemStyle: { color: c.base, opacity: 0.75 } },
                    { name: `последние 20% (${h.tail_n})`, type: "bar", data: h.tail, barCategoryGap: "0%", itemStyle: { color: c.now, opacity: 0.6 } },
                ],
            });
        }
        if (d.before_after.some((h) => h.clipped > 0)) {
            const n = document.createElement("p");
            n.className = "muted small";
            n.textContent = "Крайние 0,5% значений с каждой стороны собраны в крайние столбики — чтобы единичный выброс не сжимал «горб».";
            s.appendChild(n);
        }
    }

    // ---------- что меняется вместе ----------
    if (d.corr) {
        const k = d.corr;
        const s = section("Что меняется вместе",
            "Корреляция (Спирмен) между дневными медианами 25 самых «трендовых» признаков. Красный блок — признаки растут вместе, " +
            "синий — один растёт, другой падает. Часто это одна первопричина (износ насоса → дренаж ↑, температура ↑, время прессования ↑).");
        const cells: N[][] = [];
        k.rho.forEach((row, i) => row.forEach((v, j) => cells.push([j, i, v == null ? null : Math.round(v * 100) / 100])));
        place(s, 120 + 22 * k.names.length).setOption({
            ...common,
            grid: { left: 360, right: 70, top: 8, bottom: 170 },
            tooltip: {
                formatter: (p: any) => {
                    const [j, i, v] = p.value;
                    return `${esc(k.full[i])}<br>${esc(k.full[j])}<br>ρ = ${v == null ? "—" : v.toFixed(2)}`;
                },
            },
            xAxis: { type: "category", data: k.names, axisLabel: { color: c.muted, rotate: 45, width: 170, overflow: "truncate" }, axisLine: { show: false }, axisTick: { show: false } },
            yAxis: { type: "category", data: k.names, inverse: true, axisLabel: { color: c.text, width: 350, overflow: "truncate" }, axisLine: { show: false }, axisTick: { show: false } },
            visualMap: {
                min: -1, max: 1, calculable: false, orient: "vertical", right: 0, top: 8, itemHeight: 140,
                text: ["+1", "−1"], textStyle: { color: c.muted }, inRange: { color: c.diverging },
            },
            series: [{ type: "heatmap", data: cells, itemStyle: { borderColor: c.bg, borderWidth: 1 } }],
        });
    }

    // ---------- связи и производительность ----------
    if (d.pairs.length || d.cycle.length) {
        const s = section("Связи и производительность",
            "Цвет точки — время (тёмный — начало периода, светлый — конец). Если облако «уезжает» с цветом, при тех же условиях " +
            "узел ведёт себя иначе — признак износа.");
        if (d.pairs.length) {
            const grid = document.createElement("div");
            grid.className = "cmp-grid cmp-grid-2";
            s.appendChild(grid);
            const maxDay = Math.max(...d.pairs[0].days.map((v) => v ?? 0));
            for (const p of d.pairs) {
                const cell = document.createElement("div");
                cell.className = "cmp-cell";
                cell.innerHTML = `<div class="cmp-title"><span class="cmp-name">${esc(p.title)}</span></div>`;
                grid.appendChild(cell);
                const pts = p.x.map((x, i) => [x, p.y[i], p.days[i]]).filter(([x, y]) => x != null && y != null);
                place(cell, 280).setOption({
                    ...common,
                    grid: { left: 56, right: 60, top: 10, bottom: 44 },
                    tooltip: { formatter: (q: any) => `${esc(p.x_name)}: ${fmt(q.value[0])}<br>${esc(p.y_name)}: ${fmt(q.value[1])}<br>день ${(+q.value[2]).toFixed(1)}` },
                    xAxis: { type: "value", scale: true, name: p.x_name, nameLocation: "middle", nameGap: 26, nameTextStyle: { color: c.muted }, ...axis },
                    yAxis: { type: "value", scale: true, ...axis },
                    visualMap: {
                        dimension: 2, min: 0, max: maxDay || 1, calculable: false, orient: "vertical", right: 0, top: 10, itemHeight: 120,
                        text: ["дни", ""], textStyle: { color: c.muted }, inRange: { color: c.time },
                    },
                    series: [{ type: "scatter", symbolSize: 4, data: pts, name: p.y_name, large: pts.length > 3000 }],
                });
                // ось Y подписываем над графиком: имя длинное, сбоку не помещается
                cell.querySelector(".cmp-title")!.insertAdjacentHTML("beforeend", `<span class="muted"> · по вертикали: ${esc(p.y_name)}</span>`);
            }
        }
        if (d.cycle.length) {
            const day = d.step === "day";
            const lab = (x: string) => (day ? `${x.slice(8, 10)}.${x.slice(5, 7)}` : `${x.slice(8, 10)}.${x.slice(5, 7)} ${x.slice(11, 16)}`);
            const t = document.createElement("div");
            t.className = "cmp-title";
            t.innerHTML = `<span class="cmp-name">Длительность цикла по ${day ? "дням" : "часам"}, с</span>` +
                `<span class="muted"> · ящик — половина циклов (Q1–Q3), черта — медиана, усы — минимум и максимум</span>`;
            s.appendChild(t);
            place(s, 260).setOption({
                ...common,
                grid: { left: 50, right: 16, top: 10, bottom: 28 },
                tooltip: {
                    formatter: (q: any) => {
                        const r = d.cycle[q.dataIndex];
                        const [mn, q1, md, q3, mx] = r.box;
                        return `${lab(r.start)} · циклов ${r.n}<br>макс ${fmt(mx)}<br>Q3 ${fmt(q3)}<br><b>медиана ${fmt(md)}</b><br>Q1 ${fmt(q1)}<br>мин ${fmt(mn)}`;
                    },
                },
                xAxis: { type: "category", data: d.cycle.map((r) => lab(r.start)), ...axis },
                yAxis: { type: "value", scale: true, ...axis },
                series: [{ type: "boxplot", data: d.cycle.map((r) => r.box), itemStyle: { color: c.boxFill, borderColor: c.base } }],
            });
        }
    }

    if (!box.children.length) box.innerHTML = `<div class="muted small">недостаточно данных</div>`;
    const ro = new ResizeObserver(() => charts.forEach((ch) => ch.resize()));
    ro.observe(box);
    owned.set(box, { charts, ro });
}

function palette() {
    const css = getComputedStyle(document.documentElement);
    const v = (n: string) => css.getPropertyValue(n).trim();
    const dark = matchMedia("(prefers-color-scheme: dark)").matches;
    const mid = v("--hover") || (dark ? "#2a2a2a" : "#eeeeee");
    return {
        text: v("--text"),
        muted: v("--muted"),
        grid: v("--grid"),
        bg: v("--panel") || v("--bg"),
        base: dark ? "#9a9a95" : "#7a7a75", // норма — нейтральный серый: «как было»
        now: "#378add", // «стало» — синий, а не красный: это данные, а не тревога (ISA101)
        boxFill: dark ? "rgba(255,255,255,0.08)" : "rgba(0,0,0,0.05)",
        // −1 синий … 0 цвет фона … +1 красный; в тёмной теме промежуточные темнее
        diverging: dark ? ["#3b6ea5", "#2c4058", mid, "#5a3428", "#c8503a"] : ["#2166ac", "#92c5de", mid, "#f4a582", "#b2182b"],
        // время: от тёмного к светлому (как viridis в Python)
        time: ["#440154", "#3b528b", "#21918c", "#5ec962", "#fde725"],
    };
}

function fmt(v: N): string {
    if (v == null) return "—";
    return String(Number(v.toPrecision(4)));
}

function esc(s: string): string {
    return s.replace(/[&<>"]/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[ch]!);
}