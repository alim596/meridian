// Package orderbook implements a price-time priority limit order book.
//
// All prices are integer ticks and all quantities are integer lots: the
// matching path never touches floating point, so matching is exact and
// deterministic. The book itself is not goroutine-safe by design — each
// instrument's book is owned by a single engine goroutine (see the engine
// package), which is what makes sequencing and replay deterministic.
package orderbook

type Side int8

const (
	Bid Side = iota
	Ask
)

func (s Side) String() string {
	if s == Bid {
		return "buy"
	}
	return "sell"
}

func (s Side) Opposite() Side {
	if s == Bid {
		return Ask
	}
	return Bid
}

type OrderType uint8

const (
	Limit OrderType = iota
	Market
)

func (t OrderType) String() string {
	if t == Market {
		return "market"
	}
	return "limit"
}

type TimeInForce uint8

const (
	// GTC rests any unfilled remainder on the book.
	GTC TimeInForce = iota
	// IOC fills what it can immediately and cancels the remainder.
	IOC
)

func (t TimeInForce) String() string {
	if t == IOC {
		return "ioc"
	}
	return "gtc"
}

// Order is a live order. Once submitted the book owns it; callers must not
// mutate it afterwards.
type Order struct {
	ID        uint64
	Account   string
	Side      Side
	Type      OrderType
	TIF       TimeInForce
	Price     int64 // ticks; ignored for Market orders
	Qty       int64
	Remaining int64

	// intrusive links: position within a price level's FIFO queue
	level      *level
	next, prev *Order
}

// Trade is a single fill between an incoming (taker) order and a resting
// (maker) order. Trades always execute at the maker's price.
type Trade struct {
	Price        int64
	Qty          int64
	TakerOrderID uint64
	MakerOrderID uint64
	TakerAccount string
	MakerAccount string
	TakerSide    Side
}

// LevelUpdate reports the new total quantity resting at a price level after
// an operation. Qty == 0 means the level was removed. These are the L2 deltas
// that ultimately stream to market-data subscribers.
type LevelUpdate struct {
	Side  Side
	Price int64
	Qty   int64
}

// PriceLevel is one row of an L2 snapshot.
type PriceLevel struct {
	Price int64
	Qty   int64
}

// Result describes everything that happened when an order was submitted.
type Result struct {
	Trades      []Trade
	Updates     []LevelUpdate
	Resting     bool  // remainder rested on the book
	CanceledQty int64 // remainder canceled (IOC / unfilled market)
}
