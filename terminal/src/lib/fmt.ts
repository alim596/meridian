// Formatting helpers. Prices arrive as integer ticks; priceScale ticks = $1.

export function px(ticks: number, scale = 100): string {
  return (ticks / scale).toLocaleString("en-US", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

export function money(ticks: number, scale = 100): string {
  return "$" + px(ticks, scale);
}

export function qty(n: number): string {
  return n.toLocaleString("en-US");
}

export function pct(n: number): string {
  return `${n >= 0 ? "+" : ""}${n.toFixed(2)}%`;
}

export function dirClass(n: number): string {
  return n > 0 ? "up" : n < 0 ? "down" : "flat";
}

/** unix-ns timestamp → HH:MM:SS.mmm */
export function tapeTime(ns: number): string {
  const d = new Date(ns / 1e6);
  return d.toTimeString().slice(0, 8) + "." + String(d.getMilliseconds()).padStart(3, "0").slice(0, 1);
}

export function clockUTC(): string {
  return new Date().toISOString().slice(11, 19) + "Z";
}
