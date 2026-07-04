import { useState } from "react";
import { useStore } from "../state/store";
import { deployBot, stopBot } from "../lib/api";
import { dirClass, money } from "../lib/fmt";

const STRATEGIES = [
  { id: "momentum", label: "Momentum", blurb: "chases short-term drift — buys strength, sells weakness" },
  { id: "meanrev", label: "Mean-rev", blurb: "fades moves — sells strength, buys weakness" },
  { id: "maker", label: "Maker", blurb: "quotes both sides of the mid and earns the spread" },
];

export function BotDeck() {
  const { selected, bots, refreshPrivate } = useStore();
  const [strategy, setStrategy] = useState("momentum");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const deploy = async () => {
    setBusy(true);
    setErr(null);
    try {
      await deployBot(selected, strategy);
      await refreshPrivate();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const stop = async (id: string) => {
    try {
      await stopBot(id);
      await refreshPrivate();
    } catch {
      /* already stopped; poll reconciles */
    }
  };

  const blurb = STRATEGIES.find((s) => s.id === strategy)?.blurb ?? "";

  return (
    <section className="panel" style={{ flex: 1 }}>
      <div className="panel-head">
        <span className="panel-title">Algo Deck</span>
        <span className="micro">{bots.filter((b) => b.running).length}/3 live</span>
      </div>
      <div className="ticket" style={{ gap: 7 }}>
        <div className="seg">
          {STRATEGIES.map((s) => (
            <button
              key={s.id}
              className={strategy === s.id ? "on neutral" : ""}
              onClick={() => setStrategy(s.id)}
            >
              {s.label}
            </button>
          ))}
        </div>
        <div className="ticket-note">{blurb}. Own $250k book, own risk checks, no privileges.</div>
        <button onClick={() => void deploy()} disabled={busy}>
          {busy ? "…" : `Deploy on ${selected}`}
        </button>
        {err && <div className="ticket-result err">{err}</div>}
      </div>
      <div className="panel-body">
        {bots.length === 0 && <div className="empty">no bots deployed</div>}
        {bots.map((b) => (
          <div key={b.id} className="bot-row">
            <div className="bot-id">
              <span className={`dot ${b.running ? "live" : "dead"}`} />
              <span className="bot-name">{b.name}</span>
            </div>
            <span className={`bot-pnl ${dirClass(b.pnl)}`}>{money(Math.round(b.pnl))}</span>
            {b.running ? (
              <button className="xbtn" onClick={() => void stop(b.id)}>
                STOP
              </button>
            ) : (
              <span className="micro">stopped</span>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}
