import { useStore } from "../state/store";
import { dirClass, pct, px, qty } from "../lib/fmt";

export function Watchlist() {
  const { instruments, stats, selected, select } = useStore();

  return (
    <section className="panel area-watch">
      <div className="panel-head">
        <span className="panel-title">Instruments</span>
        <span className="micro">{instruments.length}</span>
      </div>
      <div className="panel-body">
        {instruments.map((inst) => {
          const st = stats[inst.symbol];
          return (
            <div
              key={inst.symbol}
              className={`watch-row ${selected === inst.symbol ? "sel" : ""}`}
              onClick={() => select(inst.symbol)}
            >
              <span className="watch-sym">{inst.symbol}</span>
              <span className={`watch-last ${dirClass(st?.changePct ?? 0)}`}>
                {st ? px(st.last, inst.priceScale) : "—"}
              </span>
              <span className={`watch-chg ${dirClass(st?.changePct ?? 0)}`}>
                {st ? pct(st.changePct) : ""}
              </span>
              <span className="watch-name">{inst.name}</span>
              <span className="watch-meta">
                <span>H {st ? px(st.high, inst.priceScale) : "—"}</span>
                <span>L {st ? px(st.low, inst.priceScale) : "—"}</span>
                <span>V {st ? qty(st.volume) : "—"}</span>
              </span>
            </div>
          );
        })}
      </div>
    </section>
  );
}
