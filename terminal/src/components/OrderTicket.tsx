import { useEffect, useState } from "react";
import { useStore } from "../state/store";
import { placeOrder, ApiError } from "../lib/api";
import { feed } from "../lib/feed";
import { money, px } from "../lib/fmt";

interface Props {
  pickedPrice: number | null;
}

export function OrderTicket({ pickedPrice }: Props) {
  const { selected, instruments, account, refreshPrivate } = useStore();
  const inst = instruments.find((i) => i.symbol === selected);
  const scale = inst?.priceScale ?? 100;

  const [side, setSide] = useState<"buy" | "sell">("buy");
  const [type, setType] = useState<"limit" | "market">("limit");
  const [tif, setTif] = useState<"gtc" | "ioc">("gtc");
  const [price, setPrice] = useState("");
  const [amount, setAmount] = useState("100");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  // clicking a book row loads its price into the ticket
  useEffect(() => {
    if (pickedPrice !== null) {
      setPrice((pickedPrice / scale).toFixed(2));
      setType("limit");
    }
  }, [pickedPrice, scale]);

  // reset price when switching instruments
  useEffect(() => {
    const last = feed.book(selected)?.lastPrice;
    if (last) setPrice((last / scale).toFixed(2));
    setResult(null);
  }, [selected, scale]);

  const submit = async () => {
    if (!inst) return;
    const qtyNum = Math.floor(Number(amount));
    const priceTicks = Math.round(Number(price) * scale);
    setBusy(true);
    setResult(null);
    try {
      const r = await placeOrder({
        instrument: inst.symbol,
        side,
        type,
        tif: type === "market" ? "ioc" : tif,
        price: type === "market" ? 0 : priceTicks,
        qty: qtyNum,
      });
      if (r.status === "accepted") {
        const avg = r.filledQty > 0 ? ` avg ${px(Math.round(r.notional / r.filledQty), scale)}` : "";
        setResult({
          ok: true,
          text: `#${r.orderId} filled ${r.filledQty}/${qtyNum}${avg}${r.resting ? " · resting" : ""}`,
        });
      } else {
        setResult({ ok: false, text: `rejected: ${r.reason}` });
      }
      void refreshPrivate();
    } catch (e) {
      const msg = e instanceof ApiError && e.body && typeof e.body === "object" && "reason" in e.body
        ? String((e.body as { reason?: string }).reason)
        : e instanceof Error ? e.message : String(e);
      setResult({ ok: false, text: `rejected: ${msg}` });
    } finally {
      setBusy(false);
    }
  };

  const notional = type === "limit" ? Math.round(Number(price) * scale) * Math.floor(Number(amount) || 0) : 0;

  return (
    <section className="panel" style={{ flex: "none" }}>
      <div className="panel-head">
        <span className="panel-title">Order Ticket</span>
        <span className="micro">{selected}</span>
      </div>
      <div className="ticket">
        <div className="seg">
          <button className={side === "buy" ? "on buy" : ""} onClick={() => setSide("buy")}>
            Buy
          </button>
          <button className={side === "sell" ? "on sell" : ""} onClick={() => setSide("sell")}>
            Sell
          </button>
        </div>
        <div className="seg">
          <button className={type === "limit" ? "on neutral" : ""} onClick={() => setType("limit")}>
            Limit
          </button>
          <button className={type === "market" ? "on neutral" : ""} onClick={() => setType("market")}>
            Market
          </button>
        </div>
        {type === "limit" && (
          <>
            <div className="field">
              <label>Limit price ($)</label>
              <input
                value={price}
                onChange={(e) => setPrice(e.target.value)}
                inputMode="decimal"
                placeholder="0.00"
              />
            </div>
            <div className="seg">
              <button className={tif === "gtc" ? "on neutral" : ""} onClick={() => setTif("gtc")}>
                GTC
              </button>
              <button className={tif === "ioc" ? "on neutral" : ""} onClick={() => setTif("ioc")}>
                IOC
              </button>
            </div>
          </>
        )}
        <div className="field">
          <label>Quantity (lots)</label>
          <input
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            inputMode="numeric"
            placeholder="100"
          />
        </div>
        {type === "limit" && notional > 0 && (
          <div className="ticket-note">notional {money(notional, scale)}</div>
        )}
        <button className={`submit-btn ${side}`} onClick={() => void submit()} disabled={busy || !inst}>
          {busy ? "…" : `${side} ${selected}`}
        </button>
        {result && (
          <div className={`ticket-result ${result.ok ? "ok" : "err"}`}>{result.text}</div>
        )}
        <div className="ticket-note">
          Click a book level to load its price. Orders match against live simulated flow.
        </div>
      </div>
      {account && (
        <div className="acct-grid">
          <div className="acct-cell">
            <div className="k">Cash</div>
            <div className="v">{money(account.cash)}</div>
          </div>
          <div className="acct-cell">
            <div className="k">Equity</div>
            <div className="v">{money(Math.round(account.equity))}</div>
          </div>
        </div>
      )}
    </section>
  );
}
