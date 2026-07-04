package orderbook

import "testing"

// FuzzBook drives the book with an arbitrary byte-encoded operation stream
// and asserts conservation: every lot submitted is either traded (twice,
// once per counterparty side of the ledger), canceled, or still resting.
func FuzzBook(f *testing.F) {
	f.Add([]byte{0x01, 0x10, 0x05, 0x81, 0x0f, 0x05, 0x40})
	f.Add([]byte{0xff, 0x00, 0x22, 0x13, 0x99, 0xab, 0xcd, 0xef, 0x11, 0x07})

	f.Fuzz(func(t *testing.T, data []byte) {
		b := New()
		var id uint64
		var live []uint64
		var submitted, traded, canceledNow int64

		restingQty := func() int64 {
			var sum int64
			bids, asks := b.Snapshot(1 << 20)
			for _, l := range bids {
				sum += l.Qty
			}
			for _, l := range asks {
				sum += l.Qty
			}
			return sum
		}

		for i := 0; i+3 < len(data); i += 4 {
			op, a, c, d := data[i], data[i+1], data[i+2], data[i+3]
			switch op % 4 {
			case 0, 1: // GTC limit
				id++
				side := Side(op / 4 % 2)
				price := int64(9950) + int64(a)/2*int64(2) // 9950..10077
				qty := int64(c%100) + 1
				o := &Order{ID: id, Account: "f", Side: side, Type: Limit, TIF: GTC, Price: price, Qty: qty}
				res := b.Submit(o)
				submitted += qty
				for _, tr := range res.Trades {
					traded += tr.Qty
				}
				if res.Resting {
					live = append(live, id)
				}
				canceledNow += res.CanceledQty
			case 2: // IOC or market
				id++
				side := Side(d % 2)
				qty := int64(c%100) + 1
				typ := Limit
				tif := IOC
				if d%3 == 0 {
					typ = Market
				}
				price := int64(9950) + int64(a)
				o := &Order{ID: id, Account: "f", Side: side, Type: typ, TIF: TimeInForce(tif), Price: price, Qty: qty}
				res := b.Submit(o)
				submitted += qty
				for _, tr := range res.Trades {
					traded += tr.Qty
				}
				canceledNow += res.CanceledQty
				if res.Resting {
					t.Fatal("IOC/market order rested")
				}
			case 3: // cancel
				if len(live) > 0 {
					idx := int(a) % len(live)
					if o, _, ok := b.Cancel(live[idx]); ok {
						canceledNow += o.Remaining
					}
					live = append(live[:idx], live[idx+1:]...)
				}
			}
		}

		// Conservation: submitted = 2*matched? No — each trade consumes qty
		// from BOTH the taker and a maker, i.e. 2*tr.Qty of submitted lots.
		if got, want := restingQty()+canceledNow+2*traded, submitted; got != want {
			t.Fatalf("lot conservation violated: resting+canceled+2*traded=%d, submitted=%d", got, want)
		}

		// Book must never be crossed.
		if bb, _, ok1 := b.BestBid(); ok1 {
			if ba, _, ok2 := b.BestAsk(); ok2 && bb >= ba {
				t.Fatalf("book crossed: bid %d >= ask %d", bb, ba)
			}
		}
	})
}
