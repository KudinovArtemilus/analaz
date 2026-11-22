// Графики формы цикла. HTMX вставляет на страницу <div data-cycle-chart ...>,
// этот код находит такие «островки», загружает данные и рисует графики.
//
// Что умеет (6.2ж):
//   • обзор цикла сверху всегда: позиция, усилие, давление, перекос (как в plot_cycle);
//   • пара «задание / факт» (клапаны, насосы, MB, частотник) — на ОДНОМ графике:
//     ошибка регулятора видна как зазор между линиями;
//   • курсор и зум общие: ведёшь мышью по усилию — видишь те же секунды на давлении;
//   • при приближении участок догружается с сервера подробнее — вплоть до каждого отсчёта;
//   • отметки «где в цикле отклонение», если пришли со страницы «Анализ»;
//   • дискретные сигналы полосами под графиками — что было включено в момент отклонения.
import uPlot from "./vendor/uPlot.esm.js";

// htmx подключён обычным <script> — объявляем ровно то, чем пользуемся.
declare const htmx: { onLoad(cb: (el: Element) => void): void };

interface Channel {
  index: number; // номер канала в файле
  panel: string; // одинаковый panel — один график
  role: "" | "set" | "act"; // задание / факт / одиночный канал
  name: string;
  t: (number | null)[]; // секунды от начала цикла; у каналов одной панели — общие
  v: (number | null)[]; // null — сбой датчика, на графике разрыв
}

// Отметка «где в цикле случился признак» (пришли по ссылке со страницы «Анализ»).
interface Mark {
  t: number; // секунды от начала цикла
  ch: number[]; // на графиках каких каналов ставить
  label: string; // название признака
  kind: "max" | "min" | "absdiff" | "abs";
  value: number;
}
interface SeriesResponse {
  duration: number;
  channels: Channel[];
  marks: Mark[] | null;
}

// Дискретный сигнал: интервалы «включён», секунды от начала цикла.
interface Digital {
  index: number;
  name: string;
  on: [number, number][];
}

// Панель — один график: один канал или пара «задание / факт».
interface Panel {
  key: string;
  chans: Channel[];
}

const KIND: Record<Mark["kind"], string> = {
  max: "максимум",
  min: "минимум",
  absdiff: "наибольшая разница задание − факт",
  abs: "наибольшее по модулю",
};

// Что принадлежит «островку» — чтобы потом всё убрать.
interface Entry {
  plots: uPlot[];
  ro: ResizeObserver;
  timer?: number;
}
const charts = new Map<HTMLElement, Entry>();

htmx.onLoad((root) => {
  // root — сам вставленный элемент. Он может и сам быть «островком» (при смене каналов
  // HTMX вставляет ровно <div data-cycle-chart>), а querySelectorAll ищет только ВНУТРИ.
  const boxes = [...root.querySelectorAll<HTMLElement>("[data-cycle-chart]")];
  if (root instanceof HTMLElement && root.matches("[data-cycle-chart]")) boxes.push(root);
  boxes.forEach((box) => {
    render(box).catch((err) => {
      box.innerHTML = `<div class="error">не удалось загрузить графики: ${esc(String(err))}</div>`;
    });
  });
});

// HTMX убирает старый фрагмент → уничтожаем его графики. Без этого на экране,
// открытом сутками, каждый щелчок по галочке оставлял бы в памяти мёртвые графики.
document.addEventListener("htmx:beforeCleanupElement", (e) => {
  const el = e.target as HTMLElement;
  const entry = charts.get(el);
  if (entry) {
    clearTimeout(entry.timer);
    entry.ro.disconnect();
    entry.plots.forEach((p) => p.destroy());
    charts.delete(el);
  }
});

// load — данные каналов ch с сервера; t0/t1 — участок цикла (при приближении).
async function load(box: HTMLElement, ch: string, width: number, t0?: number, t1?: number): Promise<SeriesResponse> {
  if (!ch) return { duration: 0, channels: [], marks: null }; // нечего грузить
  const params = new URLSearchParams({
    f: box.dataset.file ?? "",
    ch,
    feat: box.dataset.feat ?? "",
    n: String(Math.round(width * 1.5)), // точек чуть больше, чем пикселей
  });
  if (t0 != null && t1 != null) {
    params.set("t0", String(t0));
    params.set("t1", String(t1));
  }
  const res = await fetch(`/api/series?${params}`);
  if (!res.ok) throw new Error(await res.text());
  return (await res.json()) as SeriesResponse;
}

// panels — группируем каналы по графикам, сохраняя порядок, в котором их выбрали.
// Внутри пары сначала задание, потом факт — так одинаково на всех графиках.
function panels(chans: Channel[]): Panel[] {
  const out: Panel[] = [];
  const byKey = new Map<string, Panel>();
  for (const ch of chans) {
    let p = byKey.get(ch.panel);
    if (!p) {
      p = { key: ch.panel, chans: [] };
      byKey.set(ch.panel, p);
      out.push(p);
    }
    p.chans.push(ch);
  }
  for (const p of out) p.chans.sort((a, b) => order(a.role) - order(b.role));
  return out;
}
const order = (r: Channel["role"]) => (r === "set" ? 0 : r === "act" ? 1 : 2);

// aligned — данные панели для uPlot: [время, канал1, канал2…]; время у каналов панели общее.
function aligned(p: Panel): uPlot.AlignedData {
  return [p.chans[0].t, ...p.chans.map((c) => c.v)] as uPlot.AlignedData;
}

async function render(box: HTMLElement): Promise<void> {
  const width = Math.max(box.clientWidth, 300);
  const chSel = box.dataset.ch ?? "", chOv = box.dataset.ov ?? "";
  // обзор и выбранные каналы — отдельными запросами: если канал есть и там, и там
  // (например, отметили усилие), это два разных графика, а не одна «пара»
  const [sel, ov, dig] = await Promise.all([load(box, chSel, width), load(box, chOv, width), loadDigital(box)]);
  const data = { duration: Math.max(sel.duration, ov.duration), marks: sel.marks ?? ov.marks };
  const nOv = panels(ov.channels).length; // первые nOv графиков — обзор
  // весь цикл — к нему возвращаемся при сбросе зума
  const full = [...panels(ov.channels), ...panels(sel.channels)];
  // Ширина левой оси одна на всех графиках: иначе подписи дискретных сигналов (широкие)
  // сдвинули бы их ось времени, и одна и та же секунда на разных графиках была бы
  // в разных местах по горизонтали — сравнивать «что было включено в этот момент» глазом нельзя.
  const gutter = dig.length ? 170 : 60;

  box.innerHTML = "";
  const colors = palette();
  const syncKey = `cycle-${box.dataset.file}`;
  const marks = data.marks ?? [];
  const list = marks.length ? markList(marks) : null;
  if (list) box.appendChild(list);

  const entry: Entry = { plots: [], ro: new ResizeObserver(() => resize()) };
  // При смене масштаба по X (зум мышью, щелчок по отметке, двойной щелчок — сброс)
  // догружаем участок. Таймер: графики синхронны, и событие приходит от каждого —
  // запрос уходит один, когда пользователь остановился.
  const onZoom = (u: uPlot) => {
    clearTimeout(entry.timer);
    const { min, max } = u.scales.x;
    entry.timer = window.setTimeout(() => refine(min, max), 250);
  };
  let shown = "full"; // какой участок сейчас загружен — чтобы не грузить его повторно

  // обзор — в своём сворачиваемом блоке сверху
  let ovBox: HTMLElement | null = null;
  if (nOv) {
    const d = document.createElement("details");
    d.className = "ov";
    d.open = true;
    d.innerHTML = `<summary>Обзор цикла</summary>`;
    box.appendChild(d);
    ovBox = d;
  }

  entry.plots = full.map((p, i) => {
    const holder = document.createElement("div");
    holder.className = "plot";
    (i < nOv && ovBox ? ovBox : box).appendChild(holder);
    // номера отметок, относящихся к каналам этого графика (нумерация — как в списке)
    const mine = marks
      .map((m, k) => ({ m, n: k + 1 }))
      .filter(({ m }) => p.chans.some((c) => m.ch.includes(c.index)));
    const opts = options(p, width, data.duration, colors, i, syncKey, mine, gutter, i < nOv ? 140 : 0);
    opts.hooks = { setScale: [(u, key) => { if (key === "x") onZoom(u); }] };
    return new uPlot(opts, aligned(p), holder);
  });
  if (dig.length) {
    entry.plots.push(digitalPanel(box, dig, width, data.duration, colors, syncKey, marks, gutter));
  }
  entry.ro.observe(box);
  charts.set(box, entry);

  // щелчок по отметке в списке — приблизить все графики к этому моменту (±1 с)
  list?.addEventListener("click", (e) => {
    const b = (e.target as HTMLElement).closest<HTMLButtonElement>("button[data-t]");
    if (!b) return;
    const t = Number(b.dataset.t);
    const min = Math.max(0, t - 1), max = Math.min(data.duration, t + 1);
    entry.plots.forEach((p) => p.setScale("x", { min, max }));
  });

  // ширина окна изменилась — растягиваем графики
  function resize(): void {
    const w = Math.max(box.clientWidth, 300);
    entry.plots.forEach((p) => p.setSize({ width: w, height: p.height }));
  }

  // refine — подробные данные для участка [min, max]; весь цикл — исходные данные.
  async function refine(min?: number | null, max?: number | null): Promise<void> {
    if (min == null || max == null) return;
    const whole = min <= 0 && max >= data.duration * 0.99;
    const want = whole ? "full" : `${min}:${max}`;
    if (want === shown) return;
    shown = want;
    let ps = full;
    if (!whole) {
      // с запасом в полэкрана по краям: небольшой сдвиг не требует нового запроса
      const pad = (max - min) / 2;
      const w = Math.max(box.clientWidth, 300), t0 = Math.max(0, min - pad), t1 = max + pad;
      const [ds, dov] = await Promise.all([load(box, chSel, w, t0, t1), load(box, chOv, w, t0, t1)]);
      if (shown !== want || !charts.has(box)) return; // пока грузили, зум уже сменился
      ps = [...panels(dov.channels), ...panels(ds.channels)];
    }
    entry.plots.forEach((p, i) => {
      if (ps[i]) p.setData(aligned(ps[i]), false); // false — масштаб не трогать
    });
  }
}

async function loadDigital(box: HTMLElement): Promise<Digital[]> {
  const res = await fetch(`/api/digital?${new URLSearchParams({ f: box.dataset.file ?? "" })}`);
  if (!res.ok) throw new Error(await res.text());
  return (await res.json()) as Digital[];
}

// digitalPanel — дискретные сигналы полосами («включён» — закрашено), как в plot_cycle.
// Это обычный uPlot с той же осью X и тем же ключом синхронизации: зум и курсор общие
// с аналоговыми графиками. Полосы рисуются на холсте в хуке draw (данных-точек нет),
// подписи строк — значения оси Y. Над панелью строка: что включено под курсором.
function digitalPanel(
  box: HTMLElement, dig: Digital[], width: number, duration: number,
  c: Colors, syncKey: string, marks: Mark[], gutter: number,
): uPlot {
  const wrap = document.createElement("details");
  wrap.className = "dig";
  wrap.open = true;
  wrap.innerHTML = `<summary>Дискретные сигналы <span class="muted">${dig.length} переключались в цикле</span></summary>` +
    `<div class="dig-now muted small">наведите курсор на график — здесь будет, что включено в этот момент</div>`;
  const now = wrap.querySelector<HTMLElement>(".dig-now")!;
  const holder = document.createElement("div");
  holder.className = "plot";
  wrap.appendChild(holder);
  box.appendChild(wrap);

  const n = dig.length;
  const rowY = (k: number) => n - 1 - k; // первая строка — сверху
  const allMarks = marks.map((m, k) => ({ m, n: k + 1 }));

  const bands: uPlot.Plugin = {
    hooks: {
      draw: [(u: uPlot) => {
        const ctx = u.ctx;
        const { left, width: w } = u.bbox;
        const h = Math.abs(u.valToPos(0, "y", true) - u.valToPos(1, "y", true)); // высота строки
        ctx.save();
        ctx.beginPath();
        ctx.rect(left, u.bbox.top, w, u.bbox.height);
        ctx.clip(); // при зуме полосы не вылезают за край
        ctx.fillStyle = c.lines[0];
        dig.forEach((d, k) => {
          const y = u.valToPos(rowY(k), "y", true) - h * 0.35;
          for (const [t0, t1] of d.on) {
            const x0 = u.valToPos(t0, "x", true), x1 = u.valToPos(t1, "x", true);
            if (x1 < left || x0 > left + w) continue;
            ctx.fillRect(x0, y, Math.max(x1 - x0, 1), h * 0.7); // минимум 1 пиксель — короткий импульс не пропадёт
          }
        });
        ctx.restore();
      }],
      // курсор (свой или пришедший синхронно с аналоговых графиков) → что включено
      setCursor: [(u: uPlot) => {
        const x = u.cursor.left;
        if (x == null || x < 0) return;
        const t = u.posToVal(x, "x");
        const on = dig.filter((d) => d.on.some(([a, b]) => t >= a && t < b)).map((d) => d.name);
        now.textContent = `${t.toFixed(3)} с — включены (${on.length}): ${on.join(", ") || "ничего"}`;
      }],
    },
  };

  const axis: uPlot.Axis = {
    stroke: c.muted,
    grid: { stroke: c.grid, width: 1 },
    ticks: { stroke: c.grid, width: 1 },
    font: "11px system-ui, sans-serif",
  };
  return new uPlot({
    width,
    height: Math.max(120, n * 14 + 40),
    cursor: { sync: { key: syncKey, scales: ["x", null] }, drag: { x: true, y: false }, points: { show: false } },
    scales: {
      x: { time: false, range: (_u, min, max) => (min == null || max == null ? [0, duration] : [min, max]) },
      y: { auto: false, range: [-0.5, n - 0.5] },
    },
    legend: { show: false },
    plugins: [bands, ...(allMarks.length ? [markPlugin(allMarks, c.danger)] : [])],
    axes: [
      { ...axis, values: (_u, vals) => vals.map((v) => `${v} с`) },
      {
        ...axis,
        size: gutter,
        font: "10px system-ui, sans-serif",
        splits: () => dig.map((_d, k) => rowY(k)),
        values: (_u, vals) => vals.map((v) => short(dig[n - 1 - v]?.name ?? "")),
        grid: { stroke: c.grid, width: 1 },
      },
    ],
    // одна пустая серия: uPlot нужна хотя бы одна, а рисуем мы сами в draw
    series: [{}, { stroke: "transparent", points: { show: false } }],
  }, [[0, duration], [null, null]], holder);
}

// markList — список отметок над графиками: номер, момент, что за точка, значение.
function markList(marks: Mark[]): HTMLElement {
  const el = document.createElement("div");
  el.className = "mark-list";
  el.innerHTML = `<div class="muted small">Где в цикле отклонение (щелчок — приблизить):</div>` +
    marks.map((m, k) =>
      `<button type="button" data-t="${m.t}"><span class="mark-n">${k + 1}</span>` +
      `${m.t.toFixed(2)} с — ${esc(m.label)}: ${KIND[m.kind]} ${fmt(m.value)}</button>`).join("");
  return el;
}

function esc(s: string): string {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
}

// markPlugin — вертикальная линия и номер отметки поверх графика.
// Рисуем прямо на холсте uPlot в хуке draw: линия остаётся на месте при зуме.
function markPlugin(marks: { m: Mark; n: number }[], color: string): uPlot.Plugin {
  return {
    hooks: {
      draw: [(u: uPlot) => {
        const ctx = u.ctx;
        const { top, height, left, width } = u.bbox;
        const r = uPlot.pxRatio;
        // несколько признаков в одной точке (макс. ошибка и пик RMS одного клапана) —
        // одна линия с номерами через запятую, иначе кружки ложатся друг на друга
        const byX = new Map<number, number[]>();
        for (const { m, n } of marks) {
          const x = Math.round(u.valToPos(m.t, "x", true));
          if (x < left || x > left + width) continue; // за пределами зума
          byX.set(x, [...(byX.get(x) ?? []), n]);
        }
        ctx.save();
        ctx.font = `bold ${11 * r}px system-ui, sans-serif`;
        for (const [x, ns] of byX) {
          const text = ns.join(",");
          const w = Math.max(16 * r, ctx.measureText(text).width + 8 * r);
          ctx.strokeStyle = color;
          ctx.lineWidth = 1.5 * r;
          ctx.setLineDash([4 * r, 3 * r]);
          ctx.beginPath();
          ctx.moveTo(x, top);
          ctx.lineTo(x, top + height);
          ctx.stroke();
          ctx.setLineDash([]);
          ctx.fillStyle = color;
          ctx.beginPath();
          // roundRect есть в браузерах с 2022 г.; на старом — просто прямоугольник
          if (ctx.roundRect) ctx.roundRect(x - w / 2, top + r, w, 16 * r, 8 * r);
          else ctx.rect(x - w / 2, top + r, w, 16 * r);
          ctx.fill();
          ctx.fillStyle = "#fff";
          ctx.textAlign = "center";
          ctx.textBaseline = "middle";
          ctx.fillText(text, x, top + 9 * r + 0.5);
        }
        ctx.restore();
      }],
    },
  };
}

function options(
  p: Panel, width: number, duration: number,
  c: Colors, i: number, syncKey: string, marks: { m: Mark; n: number }[], gutter: number,
  height = 0, // 0 — по умолчанию; обзор цикла — пониже, чтобы четыре графика помещались на экран
): uPlot.Options {
  const axis: uPlot.Axis = {
    stroke: c.muted,
    grid: { stroke: c.grid, width: 1 },
    ticks: { stroke: c.grid, width: 1 },
    font: "11px system-ui, sans-serif",
  };
  const pair = p.chans.length > 1;
  const color = c.lines[i % c.lines.length];
  return {
    width,
    height: height || (pair ? 200 : 170),
    title: p.chans.map((ch) => ch.name).join("  ·  "),
    // общий ключ: курсор и выделение (зум) синхронны для всех графиков цикла
    cursor: { sync: { key: syncKey, scales: ["x", null] }, drag: { x: true, y: false } },
    // диапазон по умолчанию — весь цикл; функция (а не массив [0, duration]) нужна,
    // чтобы шкалу можно было менять программно: щелчок по отметке приближает к ней
    scales: { x: { time: false, range: (_u, min, max) => (min == null || max == null ? [0, duration] : [min, max]) } },
    legend: { show: true, live: true },
    plugins: marks.length ? [markPlugin(marks, c.danger)] : [],
    axes: [{ ...axis, values: (_u, vals) => vals.map((v) => `${v} с`) }, { ...axis, size: gutter }],
    series: [
      { label: "время, с", value: (_u, v) => (v == null ? "—" : v.toFixed(3)) },
      // задание — серым пунктиром, факт — цветной сплошной: глаз сразу видит,
      // где факт отстал от задания. Одиночный канал — как раньше, цветной.
      ...p.chans.map((ch): uPlot.Series => ({
        label: ch.role === "set" ? `${ch.name} (задание)` : ch.role === "act" ? `${ch.name} (факт)` : ch.name,
        stroke: ch.role === "set" ? c.muted : color,
        width: ch.role === "set" ? 1.2 : 1.5,
        dash: ch.role === "set" ? [5, 3] : undefined,
        spanGaps: false, // сбой датчика — разрыв, а не прямая через него
        value: (_u, v) => (v == null ? "—" : fmt(v)),
      })),
    ],
  };
}

interface Colors { lines: string[]; muted: string; grid: string; danger: string }

// Цвета берём из CSS-переменных — тёмная и светлая тема работают сами.
function palette(): Colors {
  const css = getComputedStyle(document.documentElement);
  const v = (name: string) => css.getPropertyValue(name).trim();
  return {
    lines: [v("--line-1"), v("--line-2"), v("--line-3"), v("--line-4")],
    muted: v("--muted"),
    grid: v("--grid"),
    danger: v("--danger"),
  };
}

// short — подпись строки помещается в левое поле; полное имя — в строке «что включено».
function short(s: string): string {
  return s.length > 30 ? s.slice(0, 29) + "…" : s;
}

// fmt — значение для легенды и отметок: 7 значащих цифр — ровно столько, сколько хранит
// float32 в файле. Раньше было «чем больше число, тем меньше знаков» (≥1000 — без дробной
// части), и энкодер MB 2001.67 мм показывался как 2002 — округляла программа, а не файл.
function fmt(v: number): string {
  return String(Number(v.toPrecision(7)));
}