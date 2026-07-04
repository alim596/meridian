import { useStore } from "../state/store";
import { pct } from "../lib/fmt";

function wireTime(ms: number): string {
  return new Date(ms).toTimeString().slice(0, 8);
}

export function NewsWire() {
  const news = useStore((s) => s.news);
  const select = useStore((s) => s.select);

  return (
    <section className="panel">
      <div className="panel-head">
        <span className="panel-title">Wire</span>
        <span className="micro">market-moving</span>
      </div>
      <div className="panel-body">
        {news.length === 0 && <div className="empty">awaiting first headline</div>}
        {news.map((n, i) => (
          <article
            key={n.id}
            className={`news-item ${i === 0 ? "fresh" : ""}`}
            onClick={() => n.symbol && select(n.symbol)}
            style={{ cursor: n.symbol ? "pointer" : "default" }}
            title={n.body}
          >
            <div className="news-meta">
              <span className="t">{wireTime(n.ts)}</span>
              <span className={`tag ${n.symbol ? "" : "macro"}`}>{n.symbol || "MACRO"}</span>
              <span className={`impact ${n.severity > 0 ? "up" : n.severity < 0 ? "down" : "flat"}`}>
                {pct(n.impactPct)}
              </span>
            </div>
            <div className={`news-head sev${Math.abs(n.severity)}`}>{n.headline}</div>
            {i === 0 && <div className="news-body">{n.body}</div>}
          </article>
        ))}
      </div>
    </section>
  );
}
