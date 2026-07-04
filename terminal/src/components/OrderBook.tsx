import { useMemo } from "react";
import { useStore } from "../state/store";
import { feed } from "../lib/feed";
import { px, qty } from "../lib/fmt";

const DEPTH = 11;

interface Row {
  price: number;
  size: number;
  cum: number;
}

function buildRows(side: Map<number, number>, desc: boolean): Row[] {
  const prices = [...side.keys()].sort((a, b) => (desc ? b - a : a - b)).slice(0, DEPTH);
  let cum = 0;
  return prices.map((p) => {
    const size = side.get(p)!;
    cum += size;
    return { price: p, size, cum };
  });
}

export function OrderBook({ onPricePick }: { onPricePick: (price: number) => void }) {
  const { selected, instruments } = useStore();
  useStore((s) => s.bookTick);

  const scale = instruments.find((i) => i.symbol === selected)?.priceScale ?? 100;
  const book = feed.book(selected);

  const { bids, asks, maxCum, spread, mid } = useMemo(() => {
    if (!book) return { bids: [], asks: [], maxCum: 1, spread: 0, mid: 0 };
    const bids = buildRows(book.bids, true);
    const asks = buildRows(book.asks, false);
    const bb = bids[0]?.price ?? 0;
    const ba = asks[0]?.price ?? 0;
    return {
      bids,
      asks,
      maxCum: Math.max(bids.at(-1)?.cum ?? 1, asks.at(-1)?.cum ?? 1, 1),
      spread: bb && ba ? ba - bb : 0,
      mid: bb && ba ? (bb + ba) / 2 : book.lastPrice,
    };
  }, [book, useStore.getState().bookTick]); // eslint-disable-line react-hooks/exhaustive-deps

  const last = book?.lastPrice ?? 0;

  return (
    <section className="panel">
      <div className="panel-head">
        <span className="panel-title">Order Book</span>
        <span className="micro">{selected} · L2</span>
      </div>
      <div className="panel-body book-grid">
        {/* asks render top-down: worst at top, best at the middle */}
        <div style={{ display: "flex", flexDirection: "column-reverse" }}>
          {asks.map((r) => (
            <div key={r.price} className="book-row ask" onClick={() => onPricePick(r.price)}>
              <span className="cum">{qty(r.cum)}</span>
              <span className="sz">{qty(r.size)}</span>
              <span className="px">{px(r.price, scale)}</span>
              <span className="depth" style={{ width: `${(r.cum / maxCum) * 100}%` }} />
            </div>
          ))}
        </div>
        <div className="book-mid">
          <span className={`last ${last >= mid ? "up" : "down"}`}>{px(last, scale)}</span>
          <span className="spread">
            spread {px(spread, scale)} · mid {px(mid, scale)}
          </span>
        </div>
        <div>
          {bids.map((r) => (
            <div key={r.price} className="book-row bid" onClick={() => onPricePick(r.price)}>
              <span className="cum">{qty(r.cum)}</span>
              <span className="sz">{qty(r.size)}</span>
              <span className="px">{px(r.price, scale)}</span>
              <span className="depth" style={{ width: `${(r.cum / maxCum) * 100}%` }} />
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
