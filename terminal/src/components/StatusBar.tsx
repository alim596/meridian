import { useEffect, useState } from "react";
import { useStore } from "../state/store";
import { feed } from "../lib/feed";
import { clockUTC, money } from "../lib/fmt";

export function StatusBar() {
  const { session, account, selected, metrics, msgRate } = useStore();
  useStore((s) => s.bookTick); // re-render with feed
  const [clock, setClock] = useState(clockUTC());

  useEffect(() => {
    const id = setInterval(() => setClock(clockUTC()), 500);
    return () => clearInterval(id);
  }, []);

  const book = feed.book(selected);
  const status = book?.status ?? feed.status;
  const lat = metrics?.instruments.find((i) => i.symbol === selected)?.latency;

  return (
    <header className="statusbar area-status">
      <span className="wordmark">
        MERIDIAN <small>EXCHANGE TERMINAL</small>
      </span>
      <span className="status-cell">
        <span className={`dot ${status === "live" ? "live" : status === "syncing" ? "sync" : "dead"}`} />
        <span className="v">{status.toUpperCase()}</span>
      </span>
      <span className="status-cell">
        <span className="k">seq</span>
        <span className="v">{book?.seq ?? 0}</span>
      </span>
      <span className="status-cell">
        <span className="k">feed</span>
        <span className="v">{msgRate.toFixed(0)} msg/s</span>
      </span>
      {lat && lat.count > 0 && (
        <span className="status-cell">
          <span className="k">match p50/p99</span>
          <span className="v">
            {lat.p50Us}µs / {lat.p99Us}µs
          </span>
        </span>
      )}
      <span className="spacer" />
      {account && (
        <span className="status-cell">
          <span className="k">equity</span>
          <span className="v">{money(Math.round(account.equity))}</span>
        </span>
      )}
      <span className="status-cell">
        <span className="k">acct</span>
        <span className="v">{session?.accountId ?? "—"}</span>
      </span>
      <span className="status-cell">
        <span className="v">{clock}</span>
      </span>
    </header>
  );
}
