package orderbook

// level is one price level: a FIFO queue of resting orders.
type level struct {
	price      int64
	qty        int64 // total remaining across all orders at this level
	head, tail *Order
	prev, next *level // toward worse prices
}

// sideBook holds one side of the book as a doubly-linked list of levels
// ordered best-first, plus a price index for O(1) level lookup. Insertion
// scans from the best level; real order flow clusters at the touch, so the
// scan is short in practice while keeping the structure simple and
// cache-friendly.
type sideBook struct {
	side    Side
	best    *level
	byPrice map[int64]*level
}

// better reports whether price a has priority over price b on this side.
func (s *sideBook) better(a, b int64) bool {
	if s.side == Bid {
		return a > b
	}
	return a < b
}

func (s *sideBook) getOrCreate(price int64) *level {
	if l, ok := s.byPrice[price]; ok {
		return l
	}
	l := &level{price: price}
	s.byPrice[price] = l

	// Find the first existing level that is worse than the new price.
	var prev *level
	cur := s.best
	for cur != nil && s.better(cur.price, price) {
		prev = cur
		cur = cur.next
	}
	l.prev = prev
	l.next = cur
	if prev == nil {
		s.best = l
	} else {
		prev.next = l
	}
	if cur != nil {
		cur.prev = l
	}
	return l
}

func (s *sideBook) removeLevel(l *level) {
	if l.prev == nil {
		s.best = l.next
	} else {
		l.prev.next = l.next
	}
	if l.next != nil {
		l.next.prev = l.prev
	}
	delete(s.byPrice, l.price)
	l.prev, l.next = nil, nil
}

func (l *level) push(o *Order) {
	o.level = l
	o.prev = l.tail
	o.next = nil
	if l.tail == nil {
		l.head = o
	} else {
		l.tail.next = o
	}
	l.tail = o
	l.qty += o.Remaining
}

// unlink removes an order from its level's queue without touching level.qty;
// callers adjust quantities explicitly.
func (l *level) unlink(o *Order) {
	if o.prev == nil {
		l.head = o.next
	} else {
		o.prev.next = o.next
	}
	if o.next == nil {
		l.tail = o.prev
	} else {
		o.next.prev = o.prev
	}
	o.level, o.prev, o.next = nil, nil, nil
}

// Book is a single-instrument limit order book.
type Book struct {
	bids, asks sideBook
	orders     map[uint64]*Order
}

func New() *Book {
	return &Book{
		bids:   sideBook{side: Bid, byPrice: make(map[int64]*level)},
		asks:   sideBook{side: Ask, byPrice: make(map[int64]*level)},
		orders: make(map[uint64]*Order),
	}
}

func (b *Book) sideOf(s Side) *sideBook {
	if s == Bid {
		return &b.bids
	}
	return &b.asks
}

// crosses reports whether a taker limit price is marketable against a
// resting level on the opposite side.
func crosses(takerSide Side, takerPrice, restingPrice int64) bool {
	if takerSide == Bid {
		return takerPrice >= restingPrice
	}
	return takerPrice <= restingPrice
}

// Submit matches an incoming order against the book and, for GTC limit
// orders, rests any remainder. It returns the trades, the touched L2 levels,
// and what happened to the remainder. Order IDs must be unique.
func (b *Book) Submit(o *Order) Result {
	res := Result{}
	o.Remaining = o.Qty
	opp := b.sideOf(o.Side.Opposite())
	touched := make(map[int64]struct{}, 4)

	for o.Remaining > 0 {
		lvl := opp.best
		if lvl == nil {
			break
		}
		if o.Type == Limit && !crosses(o.Side, o.Price, lvl.price) {
			break
		}
		touched[lvl.price] = struct{}{}
		for o.Remaining > 0 {
			maker := lvl.head
			if maker == nil {
				break
			}
			fill := min(o.Remaining, maker.Remaining)
			maker.Remaining -= fill
			o.Remaining -= fill
			lvl.qty -= fill
			res.Trades = append(res.Trades, Trade{
				Price:        lvl.price,
				Qty:          fill,
				TakerOrderID: o.ID,
				MakerOrderID: maker.ID,
				TakerAccount: o.Account,
				MakerAccount: maker.Account,
				TakerSide:    o.Side,
			})
			if maker.Remaining == 0 {
				lvl.unlink(maker)
				delete(b.orders, maker.ID)
			}
		}
		if lvl.qty == 0 {
			opp.removeLevel(lvl)
		}
	}

	for price := range touched {
		qty := int64(0)
		if l, ok := opp.byPrice[price]; ok {
			qty = l.qty
		}
		res.Updates = append(res.Updates, LevelUpdate{Side: o.Side.Opposite(), Price: price, Qty: qty})
	}

	if o.Remaining > 0 {
		if o.Type == Limit && o.TIF == GTC {
			own := b.sideOf(o.Side)
			lvl := own.getOrCreate(o.Price)
			lvl.push(o)
			b.orders[o.ID] = o
			res.Resting = true
			res.Updates = append(res.Updates, LevelUpdate{Side: o.Side, Price: o.Price, Qty: lvl.qty})
		} else {
			res.CanceledQty = o.Remaining
		}
	}
	return res
}

// Cancel removes a resting order. It returns the affected order and L2
// update, or ok=false if the order is unknown (already filled or canceled).
func (b *Book) Cancel(id uint64) (o *Order, upd LevelUpdate, ok bool) {
	o, ok = b.orders[id]
	if !ok {
		return nil, LevelUpdate{}, false
	}
	lvl := o.level
	side := b.sideOf(o.Side)
	lvl.qty -= o.Remaining
	lvl.unlink(o)
	delete(b.orders, id)
	if lvl.qty == 0 {
		side.removeLevel(lvl)
	}
	return o, LevelUpdate{Side: o.Side, Price: o.Price, Qty: lvl.qty}, true
}

// Order returns a resting order by ID.
func (b *Book) Order(id uint64) (*Order, bool) {
	o, ok := b.orders[id]
	return o, ok
}

// OpenOrders returns the number of resting orders.
func (b *Book) OpenOrders() int { return len(b.orders) }

func (b *Book) BestBid() (price, qty int64, ok bool) { return best(&b.bids) }
func (b *Book) BestAsk() (price, qty int64, ok bool) { return best(&b.asks) }

func best(s *sideBook) (int64, int64, bool) {
	if s.best == nil {
		return 0, 0, false
	}
	return s.best.price, s.best.qty, true
}

// Snapshot returns up to depth levels per side, best-first.
func (b *Book) Snapshot(depth int) (bids, asks []PriceLevel) {
	return walk(&b.bids, depth), walk(&b.asks, depth)
}

func walk(s *sideBook, depth int) []PriceLevel {
	out := make([]PriceLevel, 0, depth)
	for l := s.best; l != nil && len(out) < depth; l = l.next {
		out = append(out, PriceLevel{Price: l.price, Qty: l.qty})
	}
	return out
}
