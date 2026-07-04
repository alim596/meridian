// Wire types mirroring the Go API.

export interface Stats {
  symbol: string;
  last: number;
  open: number;
  high: number;
  low: number;
  volume: number;
  tradeCount: number;
  changePct: number;
}

export interface Instrument {
  symbol: string;
  name: string;
  priceScale: number;
  initPrice: number;
  stats: Stats;
}

export type Level = [price: number, qty: number];

export interface Candle {
  t: number; // unix seconds
  o: number;
  h: number;
  l: number;
  c: number;
  v: number;
}

export interface SubmitResult {
  orderId: number;
  status: "accepted" | "rejected";
  reason?: string;
  filledQty: number;
  notional: number;
  resting: boolean;
}

export interface OpenOrder {
  instrument: string;
  orderId: number;
  side: "buy" | "sell";
  price: number;
  qty: number;
  remaining: number;
  ts: number;
}

export interface Fill {
  id: number;
  instrument: string;
  orderId: number;
  side: "buy" | "sell";
  price: number;
  qty: number;
  liquidity: "maker" | "taker";
  ts: number;
}

export interface PositionView {
  qty: number;
  avgCost: number;
  realized: number;
  unrealized: number;
  mark: number;
}

export interface AccountView {
  id: string;
  name: string;
  cash: number;
  equity: number;
  positions: Record<string, PositionView>;
}

export interface Session {
  accountId: string;
  apiKey: string;
}

export interface Trade {
  price: number;
  qty: number;
  takerSide: "buy" | "sell";
  ts: number; // unix ns
  seq: number;
}

export interface NewsItem {
  id: number;
  ts: number; // unix ms
  symbol?: string; // absent = macro
  headline: string;
  body: string;
  severity: number; // -3 .. +3
  impactPct: number;
}

export interface LeaderEntry {
  name: string;
  id: string;
  kind: "trader" | "bot" | "house";
  equity: number;
  pnl: number;
}

export interface BotView {
  id: string;
  name: string;
  strategy: string;
  symbol: string;
  accountId: string;
  createdAt: number;
  running: boolean;
  equity: number;
  pnl: number;
}

export interface EngineMetrics {
  uptimeSec: number;
  goroutines: number;
  instruments: {
    symbol: string;
    orders: number;
    latency: { count: number; meanUs: number; maxUs: number; p50Us: number; p99Us: number };
  }[];
}
