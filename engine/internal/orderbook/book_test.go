package orderbook

import "testing"

var nextID uint64

func limit(side Side, price, qty int64) *Order {
	nextID++
	return &Order{ID: nextID, Account: "t", Side: side, Type: Limit, TIF: GTC, Price: price, Qty: qty}
}

func market(side Side, qty int64) *Order {
	nextID++
	return &Order{ID: nextID, Account: "t", Side: side, Type: Market, TIF: IOC, Qty: qty}
}

func TestRestAndBest(t *testing.T) {
	b := New()
	b.Submit(limit(Bid, 100, 10))
	b.Submit(limit(Bid, 101, 5))
	b.Submit(limit(Ask, 105, 7))

	p, q, ok := b.BestBid()
	if !ok || p != 101 || q != 5 {
		t.Fatalf("best bid = %d@%d ok=%v, want 5@101", q, p, ok)
	}
	p, q, ok = b.BestAsk()
	if !ok || p != 105 || q != 7 {
		t.Fatalf("best ask = %d@%d ok=%v, want 7@105", q, p, ok)
	}
}

func TestMatchAtMakerPrice(t *testing.T) {
	b := New()
	b.Submit(limit(Ask, 105, 10))
	res := b.Submit(limit(Bid, 107, 4)) // aggressive buy crosses

	if len(res.Trades) != 1 {
		t.Fatalf("trades = %d, want 1", len(res.Trades))
	}
	tr := res.Trades[0]
	if tr.Price != 105 || tr.Qty != 4 {
		t.Fatalf("trade %d@%d, want 4@105 (maker price)", tr.Qty, tr.Price)
	}
	if res.Resting {
		t.Fatal("fully filled order must not rest")
	}
	if _, q, _ := b.BestAsk(); q != 6 {
		t.Fatalf("resting ask remaining = %d, want 6", q)
	}
}

func TestTimePriorityFIFO(t *testing.T) {
	b := New()
	first := limit(Ask, 105, 5)
	second := limit(Ask, 105, 5)
	b.Submit(first)
	b.Submit(second)

	res := b.Submit(market(Bid, 7))
	if len(res.Trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(res.Trades))
	}
	if res.Trades[0].MakerOrderID != first.ID || res.Trades[0].Qty != 5 {
		t.Fatalf("first fill should exhaust earliest order: got maker=%d qty=%d", res.Trades[0].MakerOrderID, res.Trades[0].Qty)
	}
	if res.Trades[1].MakerOrderID != second.ID || res.Trades[1].Qty != 2 {
		t.Fatalf("second fill wrong: maker=%d qty=%d", res.Trades[1].MakerOrderID, res.Trades[1].Qty)
	}
}

func TestPricePriorityAcrossLevels(t *testing.T) {
	b := New()
	b.Submit(limit(Ask, 106, 5))
	b.Submit(limit(Ask, 105, 5)) // better ask arrives later, still fills first

	res := b.Submit(limit(Bid, 106, 8))
	if len(res.Trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(res.Trades))
	}
	if res.Trades[0].Price != 105 || res.Trades[1].Price != 106 {
		t.Fatalf("fill prices %d,%d; want 105 then 106", res.Trades[0].Price, res.Trades[1].Price)
	}
	if res.Trades[1].Qty != 3 {
		t.Fatalf("second fill qty = %d, want 3", res.Trades[1].Qty)
	}
}

func TestPartialFillRestsRemainder(t *testing.T) {
	b := New()
	b.Submit(limit(Ask, 105, 3))
	res := b.Submit(limit(Bid, 105, 10))

	if !res.Resting {
		t.Fatal("remainder should rest")
	}
	p, q, ok := b.BestBid()
	if !ok || p != 105 || q != 7 {
		t.Fatalf("rested bid = %d@%d, want 7@105", q, p)
	}
	if _, _, ok := b.BestAsk(); ok {
		t.Fatal("ask side should be empty")
	}
}

func TestIOCCancelsRemainder(t *testing.T) {
	b := New()
	b.Submit(limit(Ask, 105, 3))
	o := limit(Bid, 105, 10)
	o.TIF = IOC
	res := b.Submit(o)

	if res.Resting || res.CanceledQty != 7 {
		t.Fatalf("IOC: resting=%v canceled=%d, want false/7", res.Resting, res.CanceledQty)
	}
	if _, _, ok := b.BestBid(); ok {
		t.Fatal("IOC remainder must not rest")
	}
}

func TestMarketOrderOnEmptyBookCancels(t *testing.T) {
	b := New()
	res := b.Submit(market(Bid, 10))
	if len(res.Trades) != 0 || res.CanceledQty != 10 || res.Resting {
		t.Fatalf("market on empty book: %+v", res)
	}
}

func TestCancel(t *testing.T) {
	b := New()
	o := limit(Bid, 100, 10)
	b.Submit(o)
	b.Submit(limit(Bid, 100, 5))

	_, upd, ok := b.Cancel(o.ID)
	if !ok {
		t.Fatal("cancel of resting order failed")
	}
	if upd.Qty != 5 || upd.Price != 100 {
		t.Fatalf("level update after cancel = %+v, want 5@100", upd)
	}
	if _, _, ok := b.Cancel(o.ID); ok {
		t.Fatal("double cancel must fail")
	}
	if _, ok := b.Order(o.ID); ok {
		t.Fatal("canceled order still in index")
	}
}

func TestCancelRemovesEmptyLevel(t *testing.T) {
	b := New()
	o := limit(Bid, 100, 10)
	b.Submit(o)
	b.Cancel(o.ID)
	if _, _, ok := b.BestBid(); ok {
		t.Fatal("bid side should be empty after cancel")
	}
	if b.OpenOrders() != 0 {
		t.Fatalf("open orders = %d, want 0", b.OpenOrders())
	}
}

func TestNoSelfCrossAfterRest(t *testing.T) {
	// A GTC limit that doesn't cross must never trade with its own side.
	b := New()
	b.Submit(limit(Bid, 100, 10))
	res := b.Submit(limit(Bid, 99, 10))
	if len(res.Trades) != 0 {
		t.Fatal("bids must not match bids")
	}
	bids, asks := b.Snapshot(10)
	if len(bids) != 2 || len(asks) != 0 {
		t.Fatalf("snapshot: %d bids %d asks, want 2/0", len(bids), len(asks))
	}
	if bids[0].Price != 100 || bids[1].Price != 99 {
		t.Fatalf("bid ordering wrong: %+v", bids)
	}
}

// checkInvariants verifies structural integrity of the book after arbitrary
// operations: sorted levels, consistent level quantities, index agreement.
func checkInvariants(t *testing.T, b *Book) {
	t.Helper()
	for _, s := range []*sideBook{&b.bids, &b.asks} {
		var prevPrice *int64
		count := 0
		for l := s.best; l != nil; l = l.next {
			if prevPrice != nil && !s.better(*prevPrice, l.price) {
				t.Fatalf("side %v levels out of order: %d then %d", s.side, *prevPrice, l.price)
			}
			p := l.price
			prevPrice = &p
			if l.qty <= 0 {
				t.Fatalf("empty level %d left on book", l.price)
			}
			sum := int64(0)
			for o := l.head; o != nil; o = o.next {
				if o.Remaining <= 0 {
					t.Fatalf("dead order %d on level %d", o.ID, l.price)
				}
				if o.Price != l.price || o.Side != s.side {
					t.Fatalf("order %d on wrong level", o.ID)
				}
				if _, ok := b.orders[o.ID]; !ok {
					t.Fatalf("order %d on book but not in index", o.ID)
				}
				sum += o.Remaining
			}
			if sum != l.qty {
				t.Fatalf("level %d qty %d != sum of orders %d", l.price, l.qty, sum)
			}
			if got, ok := s.byPrice[l.price]; !ok || got != l {
				t.Fatalf("price index disagrees at %d", l.price)
			}
			count++
		}
		if count != len(s.byPrice) {
			t.Fatalf("side %v: %d levels linked, %d indexed", s.side, count, len(s.byPrice))
		}
	}
	for id, o := range b.orders {
		if o.level == nil {
			t.Fatalf("indexed order %d has no level", id)
		}
	}
}

func TestInvariantsAfterMixedFlow(t *testing.T) {
	b := New()
	var ids []uint64
	// deterministic pseudo-random flow
	x := uint64(88172645463325252)
	rnd := func(n int64) int64 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		v := int64(x % uint64(n))
		if v < 0 {
			v = -v
		}
		return v
	}
	for i := 0; i < 20000; i++ {
		switch rnd(10) {
		case 0, 1: // cancel something
			if len(ids) > 0 {
				idx := rnd(int64(len(ids)))
				b.Cancel(ids[idx])
				ids = append(ids[:idx], ids[idx+1:]...)
			}
		case 2: // market order
			side := Side(rnd(2))
			b.Submit(market(side, 1+rnd(500)))
		default: // limit order around mid 10000
			side := Side(rnd(2))
			price := int64(10000)
			if side == Bid {
				price -= rnd(50)
			} else {
				price += rnd(50)
			}
			// occasionally aggressive
			if rnd(5) == 0 {
				if side == Bid {
					price += 60
				} else {
					price -= 60
				}
			}
			o := limit(side, price, 1+rnd(300))
			o.Account = "flow"
			res := b.Submit(o)
			if res.Resting {
				ids = append(ids, o.ID)
			}
		}
		if i%997 == 0 {
			checkInvariants(t, b)
		}
	}
	checkInvariants(t, b)
}
