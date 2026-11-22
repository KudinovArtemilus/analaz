// Минимальные типы для урезанной сборки ECharts 6 (vendor/echarts.esm.js собран из entry с
// тепловой картой, линиями, столбиками, точками и ящиками). Опции — объект ECharts, описанный
// в документации; типизировать его целиком не стали: полный d.ts ECharts весит мегабайты.
export interface ECharts {
    setOption(option: object, notMerge?: boolean): void;
    resize(): void;
    dispose(): void;
    on(event: string, handler: (params: any) => void): void;
}
export function init(dom: HTMLElement, theme?: string | null, opts?: { renderer?: "canvas" | "svg" }): ECharts;
export function getInstanceByDom(dom: HTMLElement): ECharts | undefined;