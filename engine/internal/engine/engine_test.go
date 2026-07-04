package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/alim596/meridian/internal/orderbook"
)

type eventSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *eventSink) emit(e Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *eventSink) all() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

func newTestEngine(t *testing.T, risk RiskChecker) (*Engine, *eventSink) {
	t.Helper()
	sink := &eventSink{}
	e := New(Instrument{Symbol: "TST", PriceScale: 100, InitPrice: 10000}, risk, sink.emit)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.Run(ctx)
	return e, sink
}

func submit(e *Engine, acct string, side orderbook.Side, price, qty int64) SubmitResp {
	resp := make(chan SubmitResp, 1)
	e.Submit(SubmitCmd{
		Account: acct, Side: side, Type: orderbook.Limit, TIF: orderbook.GTC,
		Price: price, Qty: qty, Resp: resp,
	})
	return <-resp
}

func TestSubmitMatchAndEventSequencing(t *testing.T) {
	e, sink := newTestEngine(t, nil)

	r1 := submit(e, "alice", orderbook.Ask, 10010, 5)
	if r1.Status != "accepted" || !r1.Resting {
		t.Fatalf("first order: %+v", r1)
	}
	r2 := submit(e, "bob", orderbook.Bid, 10010, 3)
	if r2.FilledQty != 3 || r2.Notional != 3*10010 {
		t.Fatalf("taker fill: %+v", r2)
	}

	snap := e.Snapshot(5)
	if len(snap.Asks) != 1 || snap.Asks[0].Qty != 2 {
		t.Fatalf("snapshot asks: %+v", snap.Asks)
	}
	if snap.LastPrice != 10010 {
		t.Fatalf("last price = %d", snap.LastPrice)
	}

	events := sink.all()
	var prev uint64
	var trades int
	for _, ev := range events {
		if ev.Seq != prev+1 {
			t.Fatalf("sequence gap: %d after %d", ev.Seq, prev)
		}
		prev = ev.Seq
		if ev.Kind == EvTrade {
			trades++
			if ev.Trade.TakerAccount != "bob" || ev.Trade.MakerAccount != "alice" {
				t.Fatalf("trade accounts: %+v", ev.Trade)
			}
		}
	}
	if trades != 1 {
		t.Fatalf("trade events = %d, want 1", trades)
	}
	// snapshot seq must be >= last emitted event seq at time of read
	if snap.Seq < prev {
		t.Fatalf("snapshot seq %d < last event seq %d", snap.Seq, prev)
	}
}

func TestCancelOwnershipEnforced(t *testing.T) {
	e, _ := newTestEngine(t, nil)
	r := submit(e, "alice", orderbook.Bid, 9990, 10)

	resp := make(chan CancelResp, 1)
	e.Cancel(CancelCmd{OrderID: r.OrderID, Account: "mallory", Resp: resp})
	if c := <-resp; c.OK {
		t.Fatal("cancel by non-owner succeeded")
	}

	e.Cancel(CancelCmd{OrderID: r.OrderID, Account: "alice", Resp: resp})
	if c := <-resp; !c.OK {
		t.Fatalf("owner cancel failed: %+v", c)
	}
}

type rejectAll struct{}

func (rejectAll) CheckOrder(_, _ string, _ orderbook.Side, _ orderbook.OrderType, _, _, _ int64) error {
	return errors.New("insufficient buying power")
}

func TestRiskRejection(t *testing.T) {
	e, sink := newTestEngine(t, rejectAll{})
	r := submit(e, "alice", orderbook.Bid, 10000, 1)
	if r.Status != "rejected" || r.Reason != "insufficient buying power" {
		t.Fatalf("resp: %+v", r)
	}
	for _, ev := range sink.all() {
		if ev.Kind != EvRejected {
			t.Fatalf("unexpected event %s for rejected order", ev.Kind)
		}
	}
}

func TestValidation(t *testing.T) {
	e, _ := newTestEngine(t, nil)
	cases := []SubmitCmd{
		{Account: "a", Side: orderbook.Bid, Type: orderbook.Limit, TIF: orderbook.GTC, Price: 100, Qty: 0},
		{Account: "a", Side: orderbook.Bid, Type: orderbook.Limit, TIF: orderbook.GTC, Price: -5, Qty: 10},
		{Account: "a", Side: orderbook.Bid, Type: orderbook.Market, TIF: orderbook.GTC, Qty: 10},
		{Account: "a", Side: orderbook.Bid, Type: orderbook.Limit, TIF: orderbook.GTC, Price: 100, Qty: MaxOrderQty + 1},
	}
	for i, c := range cases {
		c.Resp = make(chan SubmitResp, 1)
		e.Submit(c)
		if r := <-c.Resp; r.Status != "rejected" {
			t.Fatalf("case %d not rejected: %+v", i, r)
		}
	}
}

func TestPublicCopyStripsAccounts(t *testing.T) {
	ev := Event{
		Kind:  EvTrade,
		Trade: &TradeEvent{Price: 1, Qty: 1, TakerAccount: "secret", MakerAccount: "secret2"},
	}
	pub := ev.PublicCopy()
	if pub.Trade.TakerAccount != "" || pub.Trade.MakerAccount != "" {
		t.Fatal("accounts leaked to public event")
	}
	if ev.Trade.TakerAccount != "secret" {
		t.Fatal("original event mutated")
	}
}
