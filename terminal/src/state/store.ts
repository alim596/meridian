import { create } from "zustand";
import type {
  AccountView, EngineMetrics, Fill, Instrument, OpenOrder, Session, Stats,
} from "../lib/types";
import * as api from "../lib/api";
import { feed } from "../lib/feed";

interface TerminalState {
  session: Session | null;
  instruments: Instrument[];
  stats: Record<string, Stats>;
  selected: string;
  account: AccountView | null;
  openOrders: OpenOrder[];
  fills: Fill[];
  metrics: EngineMetrics | null;
  bookTick: number; // bumped ≤1×/frame when feed data changes
  msgRate: number;
  error: string | null;

  boot: () => Promise<void>;
  select: (symbol: string) => void;
  refreshPrivate: () => Promise<void>;
}

export const useStore = create<TerminalState>((set, get) => ({
  session: null,
  instruments: [],
  stats: {},
  selected: "",
  account: null,
  openOrders: [],
  fills: [],
  metrics: null,
  bookTick: 0,
  msgRate: 0,
  error: null,

  boot: async () => {
    try {
      const [session, instruments] = await Promise.all([
        api.ensureSession(),
        api.getInstruments(),
      ]);
      const stats: Record<string, Stats> = {};
      for (const i of instruments) stats[i.symbol] = i.stats;
      const selected = instruments[0]?.symbol ?? "";
      set({ session, instruments, stats, selected });

      feed.connect();
      for (const i of instruments) feed.subscribe(i.symbol);

      // Batch high-frequency book updates into one render per frame.
      let dirty = false;
      feed.onChange(() => { dirty = true; });
      const raf = () => {
        if (dirty) {
          dirty = false;
          set((s) => ({ bookTick: s.bookTick + 1 }));
        }
        requestAnimationFrame(raf);
      };
      requestAnimationFrame(raf);

      feed.onStats((list) => {
        set((s) => {
          const next = { ...s.stats };
          for (const st of list) next[st.symbol] = st;
          return { stats: next };
        });
      });

      // private data + metrics polling
      const poll = async () => {
        try {
          await get().refreshPrivate();
          set({ error: null });
        } catch (e) {
          set({ error: e instanceof Error ? e.message : String(e) });
        }
      };
      void poll();
      setInterval(() => void poll(), 1500);
      setInterval(() => {
        api.getMetrics().then((m) => set({ metrics: m })).catch(() => {});
        set({ msgRate: feed.drainMsgCount() / 5 });
      }, 5000);
    } catch (e) {
      set({ error: e instanceof Error ? e.message : String(e) });
    }
  },

  select: (symbol) => set({ selected: symbol }),

  refreshPrivate: async () => {
    const [account, openOrders, fills] = await Promise.all([
      api.getAccount(),
      api.getOpenOrders(),
      api.getFills(0),
    ]);
    set({ account, openOrders, fills: fills.slice().reverse() });
  },
}));
