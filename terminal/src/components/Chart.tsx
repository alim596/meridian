import { useEffect, useRef, useState } from "react";
import {
  CandlestickSeries, ColorType, HistogramSeries, createChart,
  type IChartApi, type ISeriesApi, type UTCTimestamp,
} from "lightweight-charts";
import { useStore } from "../state/store";
import { getCandles } from "../lib/api";
import type { Candle } from "../lib/types";

const INTERVALS = ["1s", "5s", "1m"] as const;
type Interval = (typeof INTERVALS)[number];

function toSeries(c: Candle, scale: number) {
  return {
    time: c.t as UTCTimestamp,
    open: c.o / scale,
    high: c.h / scale,
    low: c.l / scale,
    close: c.c / scale,
  };
}

export function Chart() {
  const { selected, instruments } = useStore();
  const scale = instruments.find((i) => i.symbol === selected)?.priceScale ?? 100;
  const [interval, setIntervalName] = useState<Interval>("5s");

  const wrapRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candlesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const volumeRef = useRef<ISeriesApi<"Histogram"> | null>(null);

  // chart lifecycle
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const chart = createChart(el, {
      layout: {
        background: { type: ColorType.Solid, color: "#07090c" },
        textColor: "#647486",
        fontFamily: '"IBM Plex Mono", monospace',
        fontSize: 10,
        attributionLogo: false,
      },
      grid: {
        vertLines: { color: "#12171e" },
        horzLines: { color: "#12171e" },
      },
      rightPriceScale: { borderColor: "#1b232d" },
      timeScale: { borderColor: "#1b232d", timeVisible: true, secondsVisible: true },
      crosshair: {
        vertLine: { color: "#39434f", labelBackgroundColor: "#161d26" },
        horzLine: { color: "#39434f", labelBackgroundColor: "#161d26" },
      },
    });
    const candles = chart.addSeries(CandlestickSeries, {
      upColor: "#35d07f",
      downColor: "#ef5350",
      borderUpColor: "#35d07f",
      borderDownColor: "#ef5350",
      wickUpColor: "#1f7a4d",
      wickDownColor: "#8f3634",
      priceFormat: { type: "price", precision: 2, minMove: 0.01 },
    });
    const volume = chart.addSeries(HistogramSeries, {
      priceFormat: { type: "volume" },
      priceScaleId: "vol",
      color: "#263140",
    });
    chart.priceScale("vol").applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } });

    chartRef.current = chart;
    candlesRef.current = candles;
    volumeRef.current = volume;

    const ro = new ResizeObserver(() => {
      chart.applyOptions({ width: el.clientWidth, height: el.clientHeight });
    });
    ro.observe(el);
    return () => {
      ro.disconnect();
      chart.remove();
      chartRef.current = null;
    };
  }, []);

  // data: full history on symbol/interval change, then poll the live edge
  useEffect(() => {
    if (!selected) return;
    let cancelled = false;

    const load = async () => {
      const data = await getCandles(selected, interval, 600);
      if (cancelled || !candlesRef.current) return;
      candlesRef.current.setData(data.map((c) => toSeries(c, scale)));
      volumeRef.current?.setData(
        data.map((c) => ({
          time: c.t as UTCTimestamp,
          value: c.v,
          color: c.c >= c.o ? "#124430" : "#4d1a1e",
        })),
      );
      chartRef.current?.timeScale().scrollToRealTime();
    };
    void load();

    const id = setInterval(async () => {
      try {
        const tail = await getCandles(selected, interval, 2);
        if (cancelled || !candlesRef.current) return;
        for (const c of tail) {
          candlesRef.current.update(toSeries(c, scale));
          volumeRef.current?.update({
            time: c.t as UTCTimestamp,
            value: c.v,
            color: c.c >= c.o ? "#124430" : "#4d1a1e",
          });
        }
      } catch {
        /* transient — next tick retries */
      }
    }, 1000);

    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [selected, interval, scale]);

  return (
    <section className="panel">
      <div className="panel-head">
        <span className="panel-title">
          {selected} — {instruments.find((i) => i.symbol === selected)?.name ?? ""}
        </span>
        <span className="tabs">
          {INTERVALS.map((iv) => (
            <button key={iv} className={iv === interval ? "on" : ""} onClick={() => setIntervalName(iv)}>
              {iv}
            </button>
          ))}
        </span>
      </div>
      <div className="chart-wrap">
        <div ref={wrapRef} />
      </div>
    </section>
  );
}
