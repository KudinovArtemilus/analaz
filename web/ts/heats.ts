// Здоровье оборудования по плавкам → по заготовкам плавки (идея Артёма к И3).
// Сверху — карта «подсистема × плавка» (та же формула и шкала, что у дневной карты здоровья).
// Щелчок по плавке — снизу карта «подсистема × заготовка» этой плавки. Щелчок по заготовке —
// её паспорт. Данные — /api/analysis/{op}/heats[?h=…].
import { init, type ECharts } from "./vendor/echarts.esm.js";
import { palette } from "./health";

declare const htmx: { onLoad(cb: (el: Element) => void): void };

type N = number | null;
interface GH {
    heat: number; // −1 — карта по плавкам
    subsystems: string[];
    keys: number[];
    labels: string[];
    cycles: number[];
    anom: number[];
    z: N[][];
    index: N[];
    zmax: number;
    names?: string[]; // подписи столбцов (у установок штампа — время замены); нет — номера
}

const owned = new Map<HTMLElement, { charts: ECharts[]; ro: ResizeObserver }>();

htmx.onLoad((root) => {
    const boxes = [...root.querySelectorAll<HTMLElement>("[data-heats]")];
    if (root instanceof HTMLElement && root.matches("[data-heats]")) boxes.push(root);
    boxes.forEach((box) =>
        render(box).catch((err) => {
            box.innerHTML = `<div class="error">не удалось загрузить: ${String(err)}</div>`;
        }),
    );
    // И1: та же карта, столбцы — установки штампа
    const dies = [...root.querySelectorAll<HTMLElement>("[data-dies]")];
    if (root instanceof HTMLElement && root.matches("[data-dies]")) dies.push(root);
    dies.forEach((box) =>
        renderDies(box).catch((err) => {
            box.innerHTML = `<div class="error">не удалось загрузить: ${String(err)}</div>`;
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

async function load(op: string, h?: number): Promise<GH> {
    const q = h == null ? "" : `?h=${h}`;
    const res = await fetch(`/api/analysis/${encodeURIComponent(op)}/heats${q}`);
    if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
    return (await res.json()) as GH;
}

async function render(box: HTMLElement): Promise<void> {
    const op = box.dataset.op ?? "";
    const heats = await load(op);
    if (!heats.keys.length) {
        box.innerHTML = `<div class="muted small">В таблице нет номеров плавок (имена файлов без номера заготовки).</div>`;
        return;
    }
    box.innerHTML = "";
    const top = document.createElement("div");
    const title = document.createElement("div");
    title.className = "muted small hz-hint";
    title.textContent = "Щёлкните по плавке — снизу появятся её заготовки.";
    const bottom = document.createElement("div");
    box.append(top, title, bottom);

    const charts: ECharts[] = [];
    const heatChart = draw(top, heats, "плавка");
    charts.push(heatChart);
    let pieceChart: ECharts | null = null;

    heatChart.on("click", async (p: any) => {
        const h = heats.keys[p.value[0]];
        title.innerHTML = `Плавка <b>${h}</b> · загрузка заготовок…`;
        const pieces = await load(op, h);
        title.innerHTML = `Плавка <b>${h}</b>: заготовок ${pieces.keys.length}, аномальных циклов ${pieces.anom.reduce((a, b) => a + b, 0)}. ` +
            `Щелчок по заготовке — её паспорт. <a href="/piece?h=${h}">все заготовки плавки на всех операциях</a>`;
        if (pieceChart) {
            pieceChart.dispose();
            charts.splice(charts.indexOf(pieceChart), 1);
        }
        bottom.innerHTML = "";
        pieceChart = draw(bottom, pieces, "заготовка");
        charts.push(pieceChart);
        pieceChart.on("click", (q: any) => window.open(`/piece?n=${pieces.keys[q.value[0]]}`, "_blank"));
        bottom.scrollIntoView({ behavior: "smooth", block: "nearest" });
    });

    const ro = new ResizeObserver(() => charts.forEach((c) => c.resize()));
    ro.observe(box);
    owned.set(box, { charts, ro });
}

async function renderDies(box: HTMLElement): Promise<void> {
    const res = await fetch(`/api/analysis/${encodeURIComponent(box.dataset.op ?? "")}/dies`);
    if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
    const g = (await res.json()) as GH;
    box.innerHTML = "";
    if (!g.keys.length) return;
    const chart = draw(box, g, "штамп");
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(box);
    owned.set(box, { charts: [chart], ro });
}

// draw — тепловая карта «подсистема × столбец» и под ней индекс (худшая подсистема).
// Много столбцов (заготовок в плавке бывает сотня-две) — ползунок прокрутки снизу.
function draw(parent: HTMLElement, g: GH, what: "плавка" | "заготовка" | "штамп"): ECharts {
    const c = palette();
    const el = document.createElement("div");
    const mapH = 60 + 24 * g.subsystems.length;
    el.style.height = `${mapH + 170}px`;
    parent.appendChild(el);
    const many = g.keys.length > 60;
    const cells: N[][] = [];
    g.z.forEach((row, k) => row.forEach((v, b) => cells.push([b, k, v == null ? null : Math.round(v * 10) / 10])));
    const x = g.names ?? g.keys.map((k) => String(k));
    const tip = (b: number) =>
        (g.names ? `${what} <b>${g.names[b]}</b>` : `${what} <b>${g.keys[b]}</b> · с ${g.labels[b]}`) +
        `<br>рабочих циклов ${g.cycles[b]}, аномальных ${g.anom[b]}`;
    const chart = init(el);
    chart.setOption({
        textStyle: { color: c.text, fontFamily: "system-ui, sans-serif", fontSize: 11 },
        animation: false,
        tooltip: {
            formatter: (p: any) => {
                if (p.seriesType === "heatmap") {
                    const [b, k, v] = p.value;
                    return `${tip(b)}<br>${g.subsystems[k]}: ${v == null ? "нет данных" : `отклонение ${v}σ`}`;
                }
                return `${tip(p.dataIndex)}<br>худшая подсистема: ${p.value == null ? "—" : (+p.value).toFixed(1) + "σ"}`;
            },
        },
        grid: [
            { left: 210, right: 70, top: 8, height: mapH - 40 },
            { left: 210, right: 70, top: mapH + 10, height: 90 },
        ],
        xAxis: [
            { type: "category", data: x, gridIndex: 0, axisLabel: { color: c.muted, rotate: x.length > 20 ? 45 : 0 }, axisLine: { lineStyle: { color: c.grid } } },
            { type: "category", data: x, gridIndex: 1, axisLabel: { show: false }, axisLine: { lineStyle: { color: c.grid } } },
        ],
        yAxis: [
            { type: "category", data: g.subsystems, inverse: true, gridIndex: 0, axisLabel: { color: c.text }, axisLine: { show: false }, axisTick: { show: false } },
            { type: "value", gridIndex: 1, name: "индекс, σ", nameTextStyle: { color: c.muted }, axisLabel: { color: c.muted }, splitLine: { lineStyle: { color: c.grid } } },
        ],
        visualMap: {
            min: 0, max: g.zmax, seriesIndex: 0, calculable: false, orient: "vertical", right: 0, top: 8, itemHeight: 120,
            text: [`${Math.round(g.zmax)}σ`, "0"], textStyle: { color: c.muted }, inRange: { color: c.scale },
        },
        // много столбцов — ползунок; обе части карты листаются вместе
        dataZoom: many ? [{ type: "slider", xAxisIndex: [0, 1], bottom: 0, height: 18, endValue: 59 }] : [],
        series: [
            { type: "heatmap", data: cells, xAxisIndex: 0, yAxisIndex: 0, itemStyle: { borderColor: c.bg, borderWidth: 1 }, emphasis: { itemStyle: { borderColor: c.text } } },
            {
                type: "bar", data: g.index, xAxisIndex: 1, yAxisIndex: 1, itemStyle: { color: c.muted },
                // индекс за 6σ — тот же янтарный порог, что у дневной карты
                markLine: { silent: true, symbol: "none", lineStyle: { color: c.scale[3], type: "dashed" }, data: [{ yAxis: 6 }] },
            },
        ],
    });
    return chart;
}