import { useEffect, useState } from "react";
import { useStore } from "./state/store";
import { StatusBar } from "./components/StatusBar";
import { Watchlist } from "./components/Watchlist";
import { Chart } from "./components/Chart";
import { OrderBook } from "./components/OrderBook";
import { Tape } from "./components/Tape";
import { OrderTicket } from "./components/OrderTicket";
import { Blotter } from "./components/Blotter";

export default function App() {
  const boot = useStore((s) => s.boot);
  const error = useStore((s) => s.error);
  const metrics = useStore((s) => s.metrics);
  const [pickedPrice, setPickedPrice] = useState<number | null>(null);

  useEffect(() => {
    void boot();
  }, [boot]);

  return (
    <div className="app">
      <StatusBar />
      <Watchlist />
      <div className="area-center">
        <Chart />
        <Blotter />
      </div>
      <div className="area-book">
        <OrderBook onPricePick={setPickedPrice} />
        <Tape />
      </div>
      <div className="area-ticket">
        <OrderTicket pickedPrice={pickedPrice} />
      </div>
      <footer className="footbar area-foot">
        <span>
          <b>MERIDIAN</b> — simulated exchange · fictional instruments · not investment anything
        </span>
        <span className="spacer" />
        {error && <span style={{ color: "var(--red)" }}>API: {error}</span>}
        {metrics && (
          <span>
            engine uptime <b>{metrics.uptimeSec}s</b> · goroutines <b>{metrics.goroutines}</b>
          </span>
        )}
        <span>
          <b>src</b> github.com/alim596/meridian
        </span>
      </footer>
    </div>
  );
}
