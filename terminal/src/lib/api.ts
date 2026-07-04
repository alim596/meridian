import type {
  AccountView, BotView, Candle, EngineMetrics, Fill, Instrument, LeaderEntry,
  NewsItem, OpenOrder, Session, SubmitResult,
} from "./types";

const SESSION_KEY = "meridian.session.v1";

let session: Session | null = null;

function loadStoredSession(): Session | null {
  try {
    const raw = localStorage.getItem(SESSION_KEY);
    return raw ? (JSON.parse(raw) as Session) : null;
  } catch {
    return null;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (session) headers["X-API-Key"] = session.apiKey;
  const res = await fetch(path, { ...init, headers: { ...headers, ...init?.headers } });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = (body as { error?: string; reason?: string }).error ?? (body as { reason?: string }).reason ?? `HTTP ${res.status}`;
    throw new ApiError(msg, res.status, body);
  }
  return body as T;
}

export class ApiError extends Error {
  constructor(msg: string, public status: number, public body: unknown) {
    super(msg);
  }
}

/** Restores a stored session or provisions a new trading account.
 *  The exchange is stateless across restarts, so a stale key (401) triggers
 *  a fresh session transparently. */
export async function ensureSession(): Promise<Session> {
  session = loadStoredSession();
  if (session) {
    try {
      await request<AccountView>("/api/account");
      return session;
    } catch {
      session = null; // key no longer valid — engine restarted
    }
  }
  const s = await request<{ accountId: string; apiKey: string }>("/api/session", {
    method: "POST",
    body: JSON.stringify({ name: "" }),
  });
  session = { accountId: s.accountId, apiKey: s.apiKey };
  localStorage.setItem(SESSION_KEY, JSON.stringify(session));
  return session;
}

export const getInstruments = () => request<Instrument[]>("/api/instruments");
export const getCandles = (instrument: string, interval: string, limit = 500) =>
  request<Candle[]>(`/api/candles?instrument=${instrument}&interval=${interval}&limit=${limit}`);
export const getAccount = () => request<AccountView>("/api/account");
export const getOpenOrders = () => request<OpenOrder[]>("/api/orders");
export const getFills = (since: number) => request<Fill[]>(`/api/fills?since=${since}`);
export const getMetrics = () => request<EngineMetrics>("/api/metrics");

export interface OrderParams {
  instrument: string;
  side: "buy" | "sell";
  type: "limit" | "market";
  tif: "gtc" | "ioc";
  price: number;
  qty: number;
}

export const placeOrder = (p: OrderParams) =>
  request<SubmitResult>("/api/orders", { method: "POST", body: JSON.stringify(p) });

export const cancelOrder = (instrument: string, orderId: number) =>
  request<{ ok: boolean }>(`/api/orders/${instrument}/${orderId}`, { method: "DELETE" });

export const getNews = (limit = 60) => request<NewsItem[]>(`/api/news?limit=${limit}`);
export const getLeaderboard = () => request<LeaderEntry[]>("/api/leaderboard");
export const renameAccount = (name: string) =>
  request<{ name: string }>("/api/account", { method: "PATCH", body: JSON.stringify({ name }) });

export const getBots = () => request<BotView[]>("/api/bots");
export const deployBot = (instrument: string, strategy: string) =>
  request<BotView>("/api/bots", { method: "POST", body: JSON.stringify({ instrument, strategy }) });
export const stopBot = (id: string) =>
  request<{ stopped: boolean }>(`/api/bots/${id}`, { method: "DELETE" });
