// Market-data feed client implementing the exchange's snapshot + sequenced
// deltas protocol:
//
//   1. subscribe → deltas may start arriving immediately; buffer them
//   2. snapshot arrives stamped with engine seq S → reset local book,
//      replay buffered deltas with seq > S, go live
//   3. live: apply deltas; seq must be strictly increasing (the engine also
//      numbers private events, so gaps in the public stream are expected —
//      regressions are not). A regression or socket loss triggers resync.
//
// Book state lives outside React; the UI batches reads via
// requestAnimationFrame so a burst of quote updates costs one render.

import type { NewsItem, Stats, Trade } from "./types";

export type FeedStatus = "connecting" | "syncing" | "live" | "down";

interface WsMsg {
  type: "snapshot" | "l2" | "trade" | "stats" | "news" | "halt" | "resume" | "error";
  instrument?: string;
  seq?: number;
  ts?: number;
  bids?: [number, number][];
  asks?: [number, number][];
  last?: number;
  side?: "buy" | "sell";
  price?: number;
  qty?: number;
  takerSide?: "buy" | "sell";
  stats?: Stats[];
  news?: NewsItem;
  until?: number; // halt end, unix ms
  error?: string;
}

export interface BookState {
  bids: Map<number, number>;
  asks: Map<number, number>;
  lastPrice: number;
  seq: number;
  status: FeedStatus;
  haltedUntil: number; // unix ms; 0 = trading normally
}

const MAX_TAPE = 80;

class Feed {
  private ws: WebSocket | null = null;
  private books = new Map<string, BookState>();
  private pending = new Map<string, WsMsg[]>(); // deltas buffered pre-snapshot
  private tapes = new Map<string, Trade[]>();
  private subs = new Set<string>();
  private listeners = new Set<() => void>();
  private statsListeners = new Set<(s: Stats[]) => void>();
  private newsListeners = new Set<(n: NewsItem) => void>();
  private reconnectDelay = 500;
  private msgCount = 0;

  status: FeedStatus = "connecting";

  connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    this.ws = new WebSocket(`${proto}://${location.host}/ws/market`);
    this.status = "connecting";
    this.notify();

    this.ws.onopen = () => {
      this.reconnectDelay = 500;
      this.status = "live";
      // resubscribe everything after a reconnect
      for (const sym of this.subs) this.requestSub(sym);
      this.notify();
    };
    this.ws.onmessage = (ev) => this.onMessage(JSON.parse(ev.data as string) as WsMsg);
    this.ws.onclose = () => {
      this.status = "down";
      for (const b of this.books.values()) b.status = "down";
      this.notify();
      setTimeout(() => this.connect(), this.reconnectDelay);
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, 8000);
    };
    this.ws.onerror = () => this.ws?.close();
  }

  subscribe(symbol: string) {
    if (this.subs.has(symbol)) return;
    this.subs.add(symbol);
    this.requestSub(symbol);
  }

  private requestSub(symbol: string) {
    this.books.set(symbol, {
      bids: new Map(), asks: new Map(), lastPrice: 0, seq: 0, status: "syncing",
      haltedUntil: 0,
    });
    this.pending.set(symbol, []);
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ op: "subscribe", instrument: symbol }));
    }
  }

  private onMessage(m: WsMsg) {
    this.msgCount++;
    switch (m.type) {
      case "snapshot": {
        const sym = m.instrument!;
        const b = this.books.get(sym);
        if (!b) return;
        b.bids = new Map(m.bids ?? []);
        b.asks = new Map(m.asks ?? []);
        b.lastPrice = m.last ?? 0;
        b.seq = m.seq ?? 0;
        b.status = "live";
        b.haltedUntil = m.until ?? 0;
        // replay deltas that raced ahead of the snapshot
        for (const d of this.pending.get(sym) ?? []) {
          if ((d.seq ?? 0) > b.seq) this.applyDelta(b, d);
        }
        this.pending.set(sym, []);
        break;
      }
      case "l2":
      case "trade":
      case "halt":
      case "resume": {
        const sym = m.instrument!;
        const b = this.books.get(sym);
        if (!b) return;
        if (b.status === "syncing") {
          this.pending.get(sym)?.push(m);
          return;
        }
        if ((m.seq ?? 0) <= b.seq) return; // stale (pre-snapshot) delta
        this.applyDelta(b, m);
        break;
      }
      case "stats":
        if (m.stats) for (const fn of this.statsListeners) fn(m.stats);
        return;
      case "news":
        if (m.news) for (const fn of this.newsListeners) fn(m.news);
        return;
      case "error":
        console.warn("feed error:", m.error);
        return;
    }
    this.notify();
  }

  private applyDelta(b: BookState, m: WsMsg) {
    if (m.seq !== undefined && m.seq < b.seq) {
      // Sequence regression should be impossible over TCP — resync loudly
      // rather than render a corrupt book.
      console.warn("sequence regression, resyncing", m.instrument, m.seq, b.seq);
      this.requestSub(m.instrument!);
      return;
    }
    b.seq = m.seq ?? b.seq;
    if (m.type === "halt") {
      b.haltedUntil = m.until ?? 0;
    } else if (m.type === "resume") {
      b.haltedUntil = 0;
    } else if (m.type === "l2") {
      const side = m.side === "buy" ? b.bids : b.asks;
      if (m.qty === undefined || m.qty === 0) side.delete(m.price!);
      else side.set(m.price!, m.qty);
    } else if (m.type === "trade") {
      b.lastPrice = m.price!;
      const tape = this.tapes.get(m.instrument!) ?? [];
      tape.unshift({
        price: m.price!, qty: m.qty!, takerSide: m.takerSide!,
        ts: m.ts ?? Date.now() * 1e6, seq: m.seq!,
      });
      if (tape.length > MAX_TAPE) tape.pop();
      this.tapes.set(m.instrument!, tape);
    }
  }

  book(symbol: string): BookState | undefined {
    return this.books.get(symbol);
  }

  tape(symbol: string): Trade[] {
    return this.tapes.get(symbol) ?? [];
  }

  /** messages processed since last call — drives the status-bar rate meter */
  drainMsgCount(): number {
    const n = this.msgCount;
    this.msgCount = 0;
    return n;
  }

  onChange(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  onStats(fn: (s: Stats[]) => void): () => void {
    this.statsListeners.add(fn);
    return () => this.statsListeners.delete(fn);
  }

  onNews(fn: (n: NewsItem) => void): () => void {
    this.newsListeners.add(fn);
    return () => this.newsListeners.delete(fn);
  }

  private notify() {
    for (const fn of this.listeners) fn();
  }
}

export const feed = new Feed();
