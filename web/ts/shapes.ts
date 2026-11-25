// 6.2е — форма цикла (charts.py, cycle_shapes). Данные — /api/analysis/{op}/shapes.
//
//   • как меняется форма цикла со временем — по одному типичному циклу за день (час): позиция,
//     усилие, высокое давление, перекос; синие — начало периода, красные — конец. Растянутая
//     кривая усилия, сдвинутая нижняя точка, «провал» давления у поздних циклов = пресс стал
//     работать иначе;
//   • аномальные циклы против нормального — серая толстая линия — типичный цикл, цветные —
//     самые сильные аномалии: видно, в какой момент цикла и в каком сигнале они расходятся.
//
// Все графики раздела — одна диаграмма ECharts с несколькими сетками: щелчок по циклу в
// легенде скрывает его сразу на всех графиках.
import { init, type ECharts } from "./vendor/echarts.esm.js";

declare const htmx: { onLoad(cb: (el: Element) => void): void };

type N = number | null;
interface Curve { t: number[]; y: N[] }
interface Cycle { label: string; rel: string; curves: Record<string, Curve> }
interface Shapes {
    step: "day" | "hour";
    channels: string[];
    labels: string[];
    days: Cycle[];
    ref: Cycle | null;
    anomalies: Cycle[];
    skipped: number;
    missing: number;
}

const owned = new Map<HTMLElement, { charts: ECharts[]; ro: ResizeObserver }>();

htmx.onLoad((root) => {
    const boxes = [...root.querySelectorAll<HTMLElement>("[data-shapes]")];
    if (root instanceof HTMLElement && root.matches("[data-shapes]")) boxes.push(root);
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
    const res = await fetch(`/api/analysis/${encodeURIComponent(op)}/shapes`);
    if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
    const d = (await res.json()) as Shapes;
    const c = palette();
    const charts: ECharts[] = [];
    box.innerHTML = "";

    const note = (txt: string) => {
        const p = document.createElement("p");
        p.className = "muted small";
        p.textContent = txt;
        box.appendChild(p);
    };
    if (d.days.length < 2) {
        note("Нет хотя бы двух циклов с файлами: .shp уже нет ни в буфере регистратора, ни в архиве.");
        return;
    }

    // одна диаграмма: nch графиков сеткой cols × rows, по серии на (цикл, канал)
    const draw = (title: string, help: string, cycles: { c: Cycle; color: string; width: number }[], nch: number) => {
        const s = document.createElement("div");
        s.className = "cmp-sec";
        s.innerHTML = `<h4>${title}</h4><p class="muted small">${help}</p>`;
        box.appendChild(s);
        const cols = 2, rows = Math.ceil(nch / 2), h = 230, top = 70;
        const el = document.createElement("div");
        el.className = "cmp-chart";
        el.style.height = `${top + rows * h}px`;
        s.appendChild(el);
        const ch = init(el);
        charts.push(ch);
        const grids: any[] = [], xs: any[] = [], ys: any[] = [], titles: any[] = [], series: any[] = [];
        for (let k = 0; k < nch; k++) {
            const col = k % cols, row = Math.floor(k / cols);
            const g = { left: `${col * 50 + 5}%`, width: "40%", top: top + row * h + 18, height: h - 64 };
            grids.push(g);
            titles.push({ text: d.labels[k], left: `${col * 50 + 5}%`, top: top + row * h - 4, textStyle: { fontSize: 12, fontWeight: 600, color: c.text } });
            xs.push({ type: "value", gridIndex: k, name: row === rows - 1 ? "с от начала записи" : "", nameLocation: "middle", nameGap: 24, nameTextStyle: { color: c.muted }, ...axis(c) });
            ys.push({ type: "value", gridIndex: k, scale: true, ...axis(c) });
            for (const { c: cy, color, width } of cycles) {
                const cur = cy.curves[d.channels[k]];
                if (!cur) continue;
                series.push({
                    type: "line", name: cy.label, xAxisIndex: k, yAxisIndex: k, showSymbol: false, sampling: "lttb",
                    lineStyle: { color, width }, itemStyle: { color }, emphasis: { disabled: true },
                    data: cur.t.map((t, i) => [t, cur.y[i]]),
                });
            }
        }
        ch.setOption({
            textStyle: { color: c.text, fontFamily: "system-ui, sans-serif", fontSize: 11 }, animation: false,
            title: titles, grid: grids, xAxis: xs, yAxis: ys, series,
            legend: { type: "scroll", top: 0, textStyle: { color: c.muted, fontSize: 10 }, data: cycles.map((x) => x.c.label) },
            tooltip: { trigger: "item", formatter: (p: any) => `${esc(p.seriesName)}<br>${fmt(p.value[0])} с: <b>${fmt(p.value[1])}</b>` },
        });
        ch.on("click", (p: any) => { // щелчок по линии — открыть сам цикл
            const cy = cycles.find((x) => x.c.label === p.seriesName);
            if (cy?.c.rel) window.open(`/cycle?f=${encodeURIComponent(cy.c.rel)}`, "_blank");
        });
    };

    const n = d.days.length;
    const unit = d.step === "day" ? "день" : "час";
    draw("Как меняется форма цикла со временем",
        `По одному типичному циклу (ближайшему к медиане усилия) за каждый ${unit}, до 30: синие — начало периода, красные — конец. ` +
        "Растянувшаяся кривая усилия, сдвинутая нижняя точка или «провал» давления у поздних циклов — изменение поведения пресса. " +
        "Щелчок по циклу в легенде — скрыть его на всех графиках, по линии — открыть цикл.",
        d.days.map((cy, k) => {
            const f = k / Math.max(1, n - 1);
            return { c: cy, color: `rgb(${Math.round(40 + 200 * f)},${Math.round(90 + 40 * (1 - f))},${Math.round(200 * (1 - f))})`, width: 1 };
        }), Math.min(4, d.channels.length));

    if (d.ref && d.anomalies.length) {
        draw("Аномальные циклы против нормального",
            `Серая толстая линия — типичный цикл; цветные — ${d.anomalies.length} самых сильных аномалий. ` +
            "Видно, в какой момент цикла и в каком сигнале они расходятся.",
            [{ c: d.ref, color: c.base, width: 3 }, ...d.anomalies.map((cy, k) => ({ c: cy, color: c.pal[k % c.pal.length], width: 1.2 }))],
            d.channels.length);
    }
    if (d.skipped || d.missing) {
        note(`Не показано циклов: ${d.missing ? `${d.missing} — файла уже нет ни в буфере, ни в архиве` : ""}` +
            `${d.missing && d.skipped ? "; " : ""}${d.skipped ? `${d.skipped} — не успели прочитать по сети (обновите страницу: прочитанное запоминается)` : ""}.`);
    }

    const ro = new ResizeObserver(() => charts.forEach((x) => x.resize()));
    ro.observe(box);
    owned.set(box, { charts, ro });
}

function axis(c: ReturnType<typeof palette>) {
    return { axisLabel: { color: c.muted }, axisLine: { lineStyle: { color: c.grid } }, splitLine: { lineStyle: { color: c.grid } } };
}

function palette() {
    const css = getComputedStyle(document.documentElement);
    const v = (n: string) => css.getPropertyValue(n).trim();
    const dark = matchMedia("(prefers-color-scheme: dark)").matches;
    return {
        text: v("--text"),
        muted: v("--muted"),
        grid: v("--grid"),
        base: dark ? "#9a9a95" : "#7a7a75", // типичный цикл — нейтральный серый
        pal: ["#378add", "#e8a33d", "#8e6bbf", "#3aa17e", "#d0605e", "#6b8e23"],
    };
}

function fmt(v: N): string {
    if (v == null) return "—";
    return String(Number(v.toPrecision(4)));
}

function esc(s: string): string {
    return s.replace(/[&<>"]/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[ch]!);
}