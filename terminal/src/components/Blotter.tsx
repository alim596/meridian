import { useState } from "react";
import { useStore } from "../state/store";
import { cancelOrder, renameAccount } from "../lib/api";
import { dirClass, money, px, qty, tapeTime } from "../lib/fmt";

type Tab = "positions" | "orders" | "fills" | "leaders";

export function Blotter() {
  const { account, openOrders, fills, leaders, refreshPrivate } = useStore();
  const [tab, setTab] = useState<Tab>("positions");
  const [callSign, setCallSign] = useState("");

  const positions = Object.entries(account?.positions ?? {}).filter(([, p]) => p.qty !== 0 || p.realized !== 0);

  const onCancel = async (instrument: string, orderId: number) => {
    try {
      await cancelOrder(instrument, orderId);
      void refreshPrivate();
    } catch {
      /* order likely filled in the meantime; next poll reconciles */
    }
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <span className="panel-title">Blotter</span>
        <span className="tabs">
          {(["positions", "orders", "fills", "leaders"] as Tab[]).map((t) => (
            <button key={t} className={tab === t ? "on" : ""} onClick={() => setTab(t)}>
              {t}
              {t === "orders" && openOrders.length > 0 ? ` (${openOrders.length})` : ""}
            </button>
          ))}
        </span>
      </div>
      <div className="panel-body">
        {tab === "positions" &&
          (positions.length === 0 ? (
            <div className="empty">no positions — trade something</div>
          ) : (
            <table className="blot">
              <thead>
                <tr>
                  <th>Symbol</th><th>Qty</th><th>Avg cost</th><th>Mark</th>
                  <th>Unrealized</th><th>Realized</th>
                </tr>
              </thead>
              <tbody>
                {positions.map(([sym, p]) => (
                  <tr key={sym}>
                    <td>{sym}</td>
                    <td className={dirClass(p.qty)}>{qty(p.qty)}</td>
                    <td>{px(Math.round(p.avgCost))}</td>
                    <td>{px(p.mark)}</td>
                    <td className={dirClass(p.unrealized)}>{money(Math.round(p.unrealized))}</td>
                    <td className={dirClass(p.realized)}>{money(Math.round(p.realized))}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}

        {tab === "orders" &&
          (openOrders.length === 0 ? (
            <div className="empty">no open orders</div>
          ) : (
            <table className="blot">
              <thead>
                <tr>
                  <th>Symbol</th><th>Side</th><th>Price</th><th>Open/Qty</th><th>Time</th><th></th>
                </tr>
              </thead>
              <tbody>
                {openOrders.map((o) => (
                  <tr key={`${o.instrument}-${o.orderId}`}>
                    <td>{o.instrument}</td>
                    <td className={o.side === "buy" ? "up" : "down"}>{o.side.toUpperCase()}</td>
                    <td>{px(o.price)}</td>
                    <td>
                      {qty(o.remaining)}/{qty(o.qty)}
                    </td>
                    <td>{tapeTime(o.ts)}</td>
                    <td>
                      <button className="xbtn" onClick={() => void onCancel(o.instrument, o.orderId)}>
                        CXL
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}

        {tab === "leaders" && (
          <>
            <div className="callsign-row">
              <input
                placeholder="set your call sign…"
                value={callSign}
                onChange={(e) => setCallSign(e.target.value)}
                maxLength={24}
              />
              <button
                disabled={!callSign.trim()}
                onClick={() => {
                  void renameAccount(callSign.trim()).then(() => refreshPrivate());
                  setCallSign("");
                }}
              >
                SET
              </button>
            </div>
            {leaders.length === 0 ? (
              <div className="empty">leaderboard warming up</div>
            ) : (
              <table className="blot">
                <thead>
                  <tr>
                    <th>#</th><th>Trader</th><th>Kind</th><th>Equity</th><th>Session P&amp;L</th>
                  </tr>
                </thead>
                <tbody>
                  {leaders.map((l, i) => {
                    const you = account && l.name === account.name;
                    return (
                      <tr key={l.id + l.name} className={you ? "you" : ""}>
                        <td>{i + 1}</td>
                        <td>
                          {l.name}
                          {you ? " ◂ you" : ""}
                        </td>
                        <td className="micro">{l.kind}</td>
                        <td>{money(Math.round(l.equity))}</td>
                        <td className={dirClass(l.pnl)}>{money(Math.round(l.pnl))}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </>
        )}

        {tab === "fills" &&
          (fills.length === 0 ? (
            <div className="empty">no fills yet</div>
          ) : (
            <table className="blot">
              <thead>
                <tr>
                  <th>Time</th><th>Symbol</th><th>Side</th><th>Price</th><th>Qty</th><th>Liq</th>
                </tr>
              </thead>
              <tbody>
                {fills.map((f) => (
                  <tr key={f.id}>
                    <td>{tapeTime(f.ts)}</td>
                    <td>{f.instrument}</td>
                    <td className={f.side === "buy" ? "up" : "down"}>{f.side.toUpperCase()}</td>
                    <td>{px(f.price)}</td>
                    <td>{qty(f.qty)}</td>
                    <td className="micro">{f.liquidity}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}
      </div>
    </section>
  );
}
