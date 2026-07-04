package account

import (
	"testing"

	"github.com/alim596/meridian/internal/engine"
	"github.com/alim596/meridian/internal/orderbook"
)

func tradeEvent(seq uint64, instr string, price, qty int64, takerSide, takerAcct, makerAcct string) engine.Event {
	return engine.Event{
		Seq: seq, Instrument: instr, Kind: engine.EvTrade,
		Trade: &engine.TradeEvent{
			Price: price, Qty: qty, TakerSide: takerSide,
			TakerOrderID: 1, MakerOrderID: 2,
			TakerAccount: takerAcct, MakerAccount: makerAcct,
		},
	}
}

func TestSettlementCashAndPosition(t *testing.T) {
	m := NewManager()
	id, _ := m.CreateSession("test")
	sim := m.CreateSimAccount("mm")

	// buy 10 @ 100
	m.OnEvent(tradeEvent(1, "TST", 100, 10, "buy", id, sim))
	v, _ := m.Snapshot(id, map[string]int64{"TST": 110})
	if v.Cash != StartingCash-1000 {
		t.Fatalf("cash = %d, want %d", v.Cash, StartingCash-1000)
	}
	p := v.Positions["TST"]
	if p.Qty != 10 || p.AvgCost != 100 {
		t.Fatalf("position: %+v", p)
	}
	if p.Unrealized != 100 { // (110-100)*10
		t.Fatalf("unrealized = %v, want 100", p.Unrealized)
	}

	// sell 4 @ 120: realize (120-100)*4 = 80
	m.OnEvent(tradeEvent(2, "TST", 120, 4, "sell", id, sim))
	v, _ = m.Snapshot(id, map[string]int64{"TST": 120})
	p = v.Positions["TST"]
	if p.Qty != 6 || p.Realized != 80 || p.AvgCost != 100 {
		t.Fatalf("after partial close: %+v", p)
	}

	// counterparty mirrors: sim sold 10@100, bought 4@120
	sv, _ := m.Snapshot(sim, map[string]int64{"TST": 120})
	if sv.Positions["TST"].Qty != -6 {
		t.Fatalf("sim position: %+v", sv.Positions["TST"])
	}
}

func TestCrossThroughZero(t *testing.T) {
	m := NewManager()
	id, _ := m.CreateSession("t")
	sim := m.CreateSimAccount("mm")

	m.OnEvent(tradeEvent(1, "TST", 100, 5, "buy", id, sim))
	// sell 8 @ 110: close 5 (realize 50), open short 3 @ 110
	m.OnEvent(tradeEvent(2, "TST", 110, 8, "sell", id, sim))
	v, _ := m.Snapshot(id, map[string]int64{"TST": 110})
	p := v.Positions["TST"]
	if p.Qty != -3 || p.Realized != 50 || p.AvgCost != 110 {
		t.Fatalf("cross through zero: %+v", p)
	}
}

func TestRiskChecks(t *testing.T) {
	m := NewManager()
	id, _ := m.CreateSession("t")

	// buying power: notional > cash
	err := m.CheckOrder(id, "TST", orderbook.Bid, orderbook.Limit, StartingCash, 2, 0)
	if err == nil {
		t.Fatal("oversized buy passed risk check")
	}
	// position limit
	err = m.CheckOrder(id, "TST", orderbook.Ask, orderbook.Limit, 100, MaxPosition+1, 0)
	if err == nil {
		t.Fatal("position-limit breach passed risk check")
	}
	// sane order passes
	if err = m.CheckOrder(id, "TST", orderbook.Bid, orderbook.Limit, 100, 10, 0); err != nil {
		t.Fatalf("valid order rejected: %v", err)
	}
	// sim accounts bypass checks
	sim := m.CreateSimAccount("mm")
	if err = m.CheckOrder(sim, "TST", orderbook.Bid, orderbook.Limit, StartingCash, 100, 0); err != nil {
		t.Fatalf("sim account rejected: %v", err)
	}
}

func TestFillsSince(t *testing.T) {
	m := NewManager()
	id, _ := m.CreateSession("t")
	sim := m.CreateSimAccount("mm")
	m.OnEvent(tradeEvent(10, "TST", 100, 1, "buy", id, sim))
	m.OnEvent(tradeEvent(11, "TST", 101, 1, "buy", id, sim))

	all := m.Fills(id, 0)
	if len(all) != 2 {
		t.Fatalf("fills = %d, want 2", len(all))
	}
	rest := m.Fills(id, all[0].ID)
	if len(rest) != 1 || rest[0].Price != 101 {
		t.Fatalf("since-filter wrong: %+v", rest)
	}
}

func TestSessionKeyResolution(t *testing.T) {
	m := NewManager()
	id, key := m.CreateSession("t")
	got, ok := m.Resolve(key)
	if !ok || got != id {
		t.Fatalf("resolve: %s %v", got, ok)
	}
	if _, ok := m.Resolve("bogus"); ok {
		t.Fatal("bogus key resolved")
	}
}
