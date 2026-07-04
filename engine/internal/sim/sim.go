// Package sim generates realistic order flow so the exchange is a living
// market rather than an empty book.
//
// Three kinds of agents per instrument, all trading through the same engine
// command path as real users (no back doors):
//
//   - a fair-value process: mean-reverting Ornstein-Uhlenbeck around a
//     slowly drifting anchor — gives prices texture without trending off
//     to infinity;
//   - a market maker: quotes a ladder of bids and asks around fair value,
//     skewing quotes against its inventory so it mean-reverts its own
//     position (the classic Avellaneda-Stoikov intuition, simplified);
//   - takers: Poisson-arriving market orders whose direction mixes noise
//     with short-term momentum, so flow clusters the way real tape does.
package sim

import (
	"context"
	"math"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/alim596/meridian/internal/account"
	"github.com/alim596/meridian/internal/engine"
	"github.com/alim596/meridian/internal/orderbook"
)

// Shock is an exogenous market event (news): an instantaneous fair-value
// jump plus a temporary volatility regime.
type Shock struct {
	JumpFrac float64       // e.g. +0.02 = gap up 2%
	VolMult  float64       // sigma multiplier while the regime lasts
	Duration time.Duration // how long the elevated-vol regime persists
}

type Agent struct {
	eng    *engine.Engine
	acct   *account.Manager
	rng    *rand.Rand
	fv     atomic.Int64 // fair value in ticks
	prevFv atomic.Int64
	shocks chan Shock
}

// New builds the agent set for one instrument; call Start to run it.
func New(eng *engine.Engine, acct *account.Manager, seed int64) *Agent {
	a := &Agent{
		eng: eng, acct: acct,
		rng:    rand.New(rand.NewSource(seed)),
		shocks: make(chan Shock, 8),
	}
	a.fv.Store(eng.Inst.InitPrice)
	a.prevFv.Store(eng.Inst.InitPrice)
	return a
}

// Start launches all agents for the instrument.
func (a *Agent) Start(ctx context.Context) {
	go a.runFairValue(ctx)
	go a.runMarketMaker(ctx)
	go a.runTakers(ctx)
}

// ApplyShock feeds a news shock into the fair-value process. Non-blocking;
// drops if the (generous) buffer is full.
func (a *Agent) ApplyShock(s Shock) {
	select {
	case a.shocks <- s:
	default:
	}
}

// FairValue exposes the current fair value (bots and news sizing use it).
func (a *Agent) FairValue() int64 { return a.fv.Load() }

// runFairValue advances the OU process on a fixed clock; news shocks gap
// the anchor and temporarily raise volatility (a crude regime switch).
func (a *Agent) runFairValue(ctx context.Context) {
	const dt = 150 * time.Millisecond
	fv := float64(a.eng.Inst.InitPrice)
	anchor := fv
	theta := 0.05           // mean reversion strength per step
	sigma := fv * 0.0006    // per-step vol ~6bps
	anchorVol := fv * 0.0002

	volMult := 1.0
	var volUntil time.Time

	ticker := time.NewTicker(dt)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-a.shocks:
			anchor *= 1 + s.JumpFrac
			// price gaps most of the way immediately; OU pulls in the rest
			fv += (anchor - fv) * 0.75
			volMult = s.VolMult
			volUntil = time.Now().Add(s.Duration)
			a.prevFv.Store(a.fv.Load())
			a.fv.Store(int64(math.Round(fv)))
		case <-ticker.C:
			if volMult != 1.0 && time.Now().After(volUntil) {
				volMult = 1.0
			}
			anchor += anchorVol * volMult * a.rng.NormFloat64()
			fv += theta*(anchor-fv) + sigma*volMult*a.rng.NormFloat64()
			if fv < 100 { // 1.00 floor so the sim never goes degenerate
				fv = 100
				anchor = math.Max(anchor, 120)
			}
			a.prevFv.Store(a.fv.Load())
			a.fv.Store(int64(math.Round(fv)))
		}
	}
}

// runMarketMaker keeps a ladder of quotes centered on fair value, skewed
// against current inventory. Cancel-and-replace each tick keeps the code
// honest (every path exercises the engine) and makes the L2 book breathe.
func (a *Agent) runMarketMaker(ctx context.Context) {
	acctID := a.acct.CreateSimAccount("mm-" + a.eng.Inst.Symbol)
	const levels = 4
	var quoted []uint64

	ticker := time.NewTicker(time.Duration(280+a.rng.Intn(120)) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fv := a.fv.Load()
			inv := a.acct.PositionQty(acctID, a.eng.Inst.Symbol)

			// pull existing quotes
			for _, id := range quoted {
				a.eng.Cancel(engine.CancelCmd{OrderID: id, Account: acctID})
			}
			quoted = quoted[:0]

			half := maxI64(2, fv*7/10000) // ~7bps half-spread, min 2 ticks
			step := maxI64(1, half/2)
			// inventory skew: long inventory pushes quotes down to sell it off
			skew := -inv / 400

			for i := int64(0); i < levels; i++ {
				bidPx := fv - half - i*step + skew
				askPx := fv + half + i*step + skew
				if bidPx <= 0 {
					continue
				}
				size := int64(60 + a.rng.Intn(240) + int(i)*40)
				quoted = a.place(acctID, orderbook.Bid, bidPx, size, quoted)
				quoted = a.place(acctID, orderbook.Ask, askPx, size, quoted)
			}
		}
	}
}

func (a *Agent) place(acctID string, side orderbook.Side, price, qty int64, quoted []uint64) []uint64 {
	resp := make(chan engine.SubmitResp, 1)
	a.eng.Submit(engine.SubmitCmd{
		Account: acctID, Side: side, Type: orderbook.Limit, TIF: orderbook.GTC,
		Price: price, Qty: qty, Resp: resp,
	})
	r := <-resp
	if r.Status == "accepted" && r.Resting {
		return append(quoted, r.OrderID)
	}
	return quoted
}

// runTakers fires market orders with Poisson arrivals; direction blends
// momentum (recent fair-value drift) with noise.
func (a *Agent) runTakers(ctx context.Context) {
	acctID := a.acct.CreateSimAccount("flow-" + a.eng.Inst.Symbol)
	type restingOrder struct {
		id       uint64
		deadline time.Time
	}
	var resting []restingOrder
	for {
		// exponential inter-arrival, mean ~450ms
		wait := time.Duration(a.rng.ExpFloat64()*450) * time.Millisecond
		if wait > 3*time.Second {
			wait = 3 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		// expire stale passive orders so the book doesn't accrete junk
		// far from fair value as the price drifts
		now := time.Now()
		keep := resting[:0]
		for _, r := range resting {
			if now.After(r.deadline) {
				a.eng.Cancel(engine.CancelCmd{OrderID: r.id, Account: acctID})
			} else {
				keep = append(keep, r)
			}
		}
		resting = keep

		fv, prev := a.fv.Load(), a.prevFv.Load()
		pUp := 0.5
		if fv > prev {
			pUp = 0.62 // momentum: buyers chase upticks
		} else if fv < prev {
			pUp = 0.38
		}
		side := orderbook.Ask
		if a.rng.Float64() < pUp {
			side = orderbook.Bid
		}
		// lognormal-ish size: mostly small, occasionally chunky
		size := int64(math.Exp(a.rng.NormFloat64()*0.9+3.2)) + 1
		if size > 800 {
			size = 800
		}

		if a.rng.Float64() < 0.75 {
			a.eng.Submit(engine.SubmitCmd{
				Account: acctID, Side: side, Type: orderbook.Market,
				TIF: orderbook.IOC, Qty: size,
			})
		} else {
			// passive retail order a little away from fair value
			off := int64(1 + a.rng.Intn(12))
			px := fv - off
			if side == orderbook.Ask {
				px = fv + off
			}
			if px > 0 {
				resp := make(chan engine.SubmitResp, 1)
				a.eng.Submit(engine.SubmitCmd{
					Account: acctID, Side: side, Type: orderbook.Limit,
					TIF: orderbook.GTC, Price: px, Qty: size, Resp: resp,
				})
				if r := <-resp; r.Resting {
					ttl := time.Duration(20+a.rng.Intn(70)) * time.Second
					resting = append(resting, restingOrder{id: r.OrderID, deadline: now.Add(ttl)})
				}
			}
		}
	}
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
