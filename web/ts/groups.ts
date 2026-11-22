// Группы однотипных узлов (6.2в, charts.py раздел 3): шесть дренажей MD1–MD6, двенадцать
// насосов, одиннадцать сервоклапанов… на одном графике. У однотипных узлов линии должны идти
// рядом — тот, кто отбился от остальных, и есть кандидат на износ.
//
// Слева — медиана признака за день (час) в реальных единицах, справа — она же в σ от нормы
// с серой полосой ±3σ. Щелчок по имени в легенде прячет линию сразу на обоих графиках
// (у серий одинаковые имена — ECharts переключает их вместе). Данные — /api/analysis/{op}/groups.
import { init, type ECharts } from "./vendor/echarts.esm.js";

declare const htmx: { onLoad(cb: (el: Element) => void): void };

interface Feat {
    feature: string;
    name: string;
    val: (number | null)[];
    z: (number | null)[];
    last: number | null;
}
interface Group {
    title: string;
    note: string;
    out: number; // линий за ±3σ в последнем интервале
    feats: Feat[];
}
interface Groups {
    step: "day" | "hour";
    bins: string[];
    groups: Group[];
}

const charts = new Map<HTMLElement, { chart: ECharts; ro: ResizeObserver }>();

htmx.onLoad((root) => {
    const boxes = [...root.querySelectorAll<HTMLElement>("[data-groups]")];
    if (root instanceof HTMLElement && root.matches("[data-groups]")) boxes.push(root);
    boxes.forEach((box) =>
        render(box).catch((err) => {
            box.innerHTML = `<div class="error">не удалось загрузить группы: ${String(err)}</div>`;
        }),
    );
});

document.addEventListener("htmx:beforeCleanupElement", (e) => {
    const c = charts.get(e.target as HTMLElement);
    if (c) {
        c.ro.disconnect();
        c.chart.dispose();
        charts.delete(e.target as HTMLElement);
    }
});

async function render(box: HTMLElement): Promise<void> {
    const op = box.dataset.op ?? "";
    const res = await fetch(`/api/analysis/${encodeURIComponent(op)}/groups`);
    if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
    const g = (await res.json()) as Groups;
    if (!g.groups.length || !g.bins.length) {
        box.innerHTML = `<div class="muted small">нет данных по группам</div>`;
        return;
    }

    // Список групп. Где в последний день есть линии за ±3σ — это написано в самом пункте:
    // по ISA101 внимание привлекает отклонение, а не раскраска всего подряд.
    const sel = document.createElement("select");
    sel.className = "grp-select";
    g.groups.forEach((gr, i) => {
        const o = document.createElement("option");
        o.value = String(i);
        o.textContent = gr.out ? `${gr.title} — ${gr.out} вне нормы` : gr.title;
        sel.appendChild(o);
    });
    // открываем первую группу с отклонением: туда и надо смотреть
    const first = Math.max(0, g.groups.findIndex((gr) => gr.out > 0));
    sel.value = String(first);

    const note = document.createElement("p");
    note.className = "muted small";
    const el = document.createElement("div");
    el.className = "grp-chart";
    box.innerHTML = "";
    box.append(sel, note, el);

    const chart = init(el);
    const show = (i: number) => {
        const gr = g.groups[i];
        note.textContent = gr.note || " ";
        chart.setOption(option(gr, g), true); // true — заменить целиком, а не слить с прошлой группой
    };
    sel.addEventListener("change", () => show(Number(sel.value)));
    show(first);

    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(box);
    charts.set(box, { chart, ro });
}

function option(gr: Group, g: Groups): object {
    const c = palette();
    const day = g.step === "day";
    const unit = day ? "день" : "час";
    const label = (s: string) => (day ? `${s.slice(8, 10)}.${s.slice(5, 7)}` : `${s.slice(8, 10)}.${s.slice(5, 7)} ${s.slice(11, 16)}`);
    const x = (gridIndex: number) => ({
        type: "category" as const,
        gridIndex,
        data: g.bins.map(label),
        axisLabel: { color: c.muted },
        axisLine: { lineStyle: { color: c.grid } },
    });
    const y = (gridIndex: number) => ({
        type: "value" as const,
        gridIndex,
        scale: true, // не от нуля: дренаж 0.50–0.55 бар иначе был бы прямой линией
        axisLabel: { color: c.muted },
        splitLine: { lineStyle: { color: c.grid } },
    });
    const fmt = (v: any) => (v == null ? "—" : (+v).toPrecision(4));
    const names = legendNames(gr.feats.map((f) => f.name));
    const series: object[] = [];
    gr.feats.forEach((f, i) => {
        const color = c.cats[i % c.cats.length];
        // линия, ушедшая за ±3σ в последний день, — толще и непрозрачная, остальные приглушены:
        // взгляд сразу падает на отбившийся узел, а не на тот, у которого цвет ярче
        const out = f.last != null && Math.abs(f.last) > 3;
        const style = { color, width: out ? 2.6 : 1.3, opacity: out || !gr.out ? 1 : 0.55 };
        series.push({
            name: names[i], type: "line", xAxisIndex: 0, yAxisIndex: 0, data: f.val,
            symbol: "circle", symbolSize: out ? 5 : 3, lineStyle: style, itemStyle: { color, opacity: style.opacity },
            connectNulls: false, // нет данных за день — разрыв, а не прямая через него
            z: out ? 3 : 2, // поверх остальных
        });
        series.push({
            name: names[i], type: "line", xAxisIndex: 1, yAxisIndex: 1, data: f.z,
            symbol: "none", lineStyle: style, itemStyle: { color },
            connectNulls: false,
            z: out ? 3 : 2,
            // полоса нормы ±3σ — один раз, на первой серии
            ...(i === 0 ? { markArea: { silent: true, itemStyle: { color: c.band }, data: [[{ yAxis: -3 }, { yAxis: 3 }]] } } : {}),
        });
    });
    return {
        textStyle: { color: c.text, fontFamily: "system-ui, sans-serif", fontSize: 11 },
        animation: false,
        title: [
            { text: `Медиана за ${unit} (реальные значения)`, left: 0, textStyle: { color: c.text, fontSize: 12, fontWeight: 500 } },
            { text: "Отклонение от нормы, σ (серое — ±3σ)", left: "64%", textStyle: { color: c.text, fontSize: 12, fontWeight: 500 } },
        ],
        legend: { type: "scroll", bottom: 0, textStyle: { color: c.text }, pageTextStyle: { color: c.muted } },
        tooltip: { trigger: "axis", valueFormatter: fmt },
        grid: [
            { left: 56, right: "39%", top: 36, bottom: 64 },
            { left: "64%", right: 16, top: 36, bottom: 64 },
        ],
        xAxis: [x(0), x(1)],
        yAxis: [y(0), y(1)],
        series,
    };
}

// legendNames — подписи для легенды. В группе у всех линий одинаковые слова в начале и
// в конце («Давление дренажа корпуса MD1, среднее», «… MD2, среднее»; «Насос 1.1: ошибка
// угла наклона под нагрузкой», «Насос 1.2: …»): что это за признак, уже написано в названии
// группы, поэтому оставляем только то, чем линии различаются, — «MD1», «Насос 1.1».
function legendNames(names: string[]): string[] {
    if (names.length < 2) return names;
    // 1) «Узел: что меряем» с одинаковым «что меряем» — оставляем узел целиком («Насос 1.1»)
    const colon = names.map((n) => n.split(/:\s*/));
    if (colon.every((p) => p.length > 1 && p.slice(1).join(":") === colon[0].slice(1).join(":"))) {
        return colon.map((p) => p[0]);
    }
    // 2) иначе убираем общие слова в начале и в конце
    const words = names.map((n) => n.split(" "));
    let pre = 0;
    while (words.every((w) => w.length > pre + 1 && w[pre] === words[0][pre])) pre++;
    let suf = 0;
    while (words.every((w) => w.length > pre + suf + 1 && w[w.length - 1 - suf] === words[0][words[0].length - 1 - suf])) suf++;
    const out = words.map((w) => w.slice(pre, w.length - suf).join(" ").replace(/[,;:]$/, ""));
    return new Set(out).size === out.length && out.every((x) => x) ? out : names; // не различимы — как было
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
        // 12 различимых цветов — в группе бывает 12 насосов; та же палитра, что у подсистем
        cats: ["#378add", "#d85a30", "#1d9e75", "#7f77dd", "#ba7517", "#d4537e", "#5f5e5a", "#639922", "#0f6e56", "#993c1d", "#534ab7", "#888780"],
    };
}