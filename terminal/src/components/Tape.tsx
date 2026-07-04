import { useStore } from "../state/store";
import { feed } from "../lib/feed";
import { px, qty, tapeTime } from "../lib/fmt";

export function Tape() {
  const { selected, instruments } = useStore();
  useStore((s) => s.bookTick);

  const scale = instruments.find((i) => i.symbol === selected)?.priceScale ?? 100;
  const trades = feed.tape(selected);

  return (
    <section className="panel">
      <div className="panel-head">
        <span className="panel-title">Time &amp; Sales</span>
        <span className="micro">{trades.length ? `${trades.length} prints` : ""}</span>
      </div>
      <div className="panel-body">
        {trades.length === 0 && <div className="empty">awaiting prints</div>}
        {trades.map((t) => (
          <div key={t.seq} className="tape-row">
            <span className="t">{tapeTime(t.ts)}</span>
            <span className={`px ${t.takerSide === "buy" ? "up" : "down"}`}>
              {px(t.price, scale)} {t.takerSide === "buy" ? "▲" : "▼"}
            </span>
            <span className="sz">{qty(t.qty)}</span>
          </div>
        ))}
      </div>
    </section>
  );
}
