package engine

// Event is the single sequenced record type emitted by an engine. Every
// mutation of an instrument's state becomes exactly one or more Events with
// a strictly increasing Seq, which is what makes the journal replayable and
// lets market-data clients detect gaps.
type Event struct {
	Seq        uint64      `json:"seq"`
	Instrument string      `json:"instrument"`
	TS         int64       `json:"ts"` // unix nanoseconds
	Kind       string      `json:"kind"`
	Order      *OrderEvent `json:"order,omitempty"`
	Trade      *TradeEvent `json:"trade,omitempty"`
	Level      *LevelEvent `json:"level,omitempty"`
	Halt       *HaltEvent  `json:"haltInfo,omitempty"`
}

// Event kinds.
const (
	EvAccepted = "accepted"
	EvRejected = "rejected"
	EvCanceled = "canceled"
	EvTrade    = "trade"
	EvL2       = "l2"
	EvHalt     = "halt"
	EvResume   = "resume"
)

// OrderEvent carries order lifecycle detail. Account is private data: the
// public WS feed strips it, only the journal and account manager see it.
type OrderEvent struct {
	ID        uint64 `json:"id"`
	Account   string `json:"account,omitempty"`
	Side      string `json:"side"`
	Type      string `json:"type"`
	TIF       string `json:"tif"`
	Price     int64  `json:"price,omitempty"`
	Qty       int64  `json:"qty"`
	Filled    int64  `json:"filled"`
	Remaining int64  `json:"remaining"`
	Reason    string `json:"reason,omitempty"`
}

type TradeEvent struct {
	Price        int64  `json:"price"`
	Qty          int64  `json:"qty"`
	TakerSide    string `json:"takerSide"`
	TakerOrderID uint64 `json:"takerOrderId"`
	MakerOrderID uint64 `json:"makerOrderId"`
	TakerAccount string `json:"takerAccount,omitempty"`
	MakerAccount string `json:"makerAccount,omitempty"`
}

type LevelEvent struct {
	Side  string `json:"side"`
	Price int64  `json:"price"`
	Qty   int64  `json:"qty"`
}

// HaltEvent records a volatility circuit breaker firing: the trade that
// tripped it, the reference price it was measured against, and when
// continuous trading resumes.
type HaltEvent struct {
	Until      int64   `json:"until"` // unix ms
	RefPrice   int64   `json:"refPrice"`
	TradePrice int64   `json:"tradePrice"`
	MovePct    float64 `json:"movePct"`
}

// PublicCopy returns the event with account identifiers removed, safe to
// broadcast on the anonymous market-data feed.
func (e Event) PublicCopy() Event {
	if e.Trade != nil {
		t := *e.Trade
		t.TakerAccount, t.MakerAccount = "", ""
		e.Trade = &t
	}
	if e.Order != nil {
		o := *e.Order
		o.Account = ""
		e.Order = &o
	}
	return e
}
