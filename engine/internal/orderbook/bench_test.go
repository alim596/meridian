package orderbook

import (
	"math/rand"
	"testing"
)

// BenchmarkSubmit measures sustained order throughput with a realistic mix:
// mostly passive limits near the touch, some aggressive crosses, some
// cancels. Run with: go test -bench=. -benchmem ./internal/orderbook
func BenchmarkSubmit(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	book := New()
	var id uint64
	var live []uint64

	// pre-warm the book
	for i := 0; i < 5000; i++ {
		id++
		side := Side(rng.Intn(2))
		price := int64(10000)
		if side == Bid {
			price -= rng.Int63n(40)
		} else {
			price += rng.Int63n(40)
		}
		o := &Order{ID: id, Side: side, Type: Limit, TIF: GTC, Price: price, Qty: 1 + rng.Int63n(200)}
		if book.Submit(o).Resting {
			live = append(live, id)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := rng.Intn(10)
		switch {
		case r < 2 && len(live) > 0: // cancel
			idx := rng.Intn(len(live))
			book.Cancel(live[idx])
			live[idx] = live[len(live)-1]
			live = live[:len(live)-1]
		case r < 4: // aggressive
			id++
			side := Side(rng.Intn(2))
			price := int64(10000)
			if side == Bid {
				price += 50
			} else {
				price -= 50
			}
			o := &Order{ID: id, Side: side, Type: Limit, TIF: IOC, Price: price, Qty: 1 + rng.Int63n(100)}
			book.Submit(o)
		default: // passive
			id++
			side := Side(rng.Intn(2))
			price := int64(10000)
			if side == Bid {
				price -= rng.Int63n(40)
			} else {
				price += rng.Int63n(40)
			}
			o := &Order{ID: id, Side: side, Type: Limit, TIF: GTC, Price: price, Qty: 1 + rng.Int63n(200)}
			if book.Submit(o).Resting {
				live = append(live, id)
			}
		}
	}
}
