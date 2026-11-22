// Точка входа фронтенда: всё, что собирается в web/static/app.js.
// Каждый модуль сам находит свои «островки» на странице через htmx.onLoad.
import "./cycle";
import "./analysis";
import "./health";
import "./groups";
import "./compare";
import "./prestops";
import "./hourly";
import "./heats";

// По умолчанию HTMX молча выбрасывает ответы 4xx/5xx — и пользователь видит пустое место
// вместо причины («файл не найден», «недопустимый путь»). Сервер на ошибку отдаёт
// готовый <div class="error">, поэтому вставляем его как обычный ответ.
// app.js подключён с defer: он выполняется до DOMContentLoaded, то есть до первых
// запросов HTMX (hx-trigger="load"), — настройка успевает примениться.
declare const htmx: { config: { responseHandling: { code: string; swap: boolean; error?: boolean }[] } };
htmx.config.responseHandling = [
    { code: "204", swap: false },
    { code: "[23]..", swap: true },
    { code: "[45]..", swap: true, error: true },
];