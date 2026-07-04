// Package news is the exchange's story generator. On a Poisson clock it
// composes fictional wire headlines about the listed companies (or the
// broader macro tape), assigns them a severity, and injects the matching
// shock into each affected instrument's fair-value process — a jump plus a
// temporary volatility regime. Prices don't just wiggle; things *happen*,
// and you can watch the book absorb them (or trip the circuit breaker).
package news

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/alim596/meridian/internal/sim"
)

type Item struct {
	ID        int64   `json:"id"`
	TS        int64   `json:"ts"` // unix ms
	Symbol    string  `json:"symbol,omitempty"` // empty = macro, hits everything
	Headline  string  `json:"headline"`
	Body      string  `json:"body"`
	Severity  int     `json:"severity"`  // -3 .. +3
	ImpactPct float64 `json:"impactPct"` // applied fair-value jump
}

type template struct {
	head string // %s = company name
	body string
	sev  int
}

var positive = []template{
	{"%s wins multi-year defense avionics contract", "Sources describe the award as the largest in the company's history; backlog roughly doubles.", 2},
	{"%s beats on earnings, raises full-year guidance", "Margins expanded for a fourth straight quarter; management called demand \"structurally underestimated\".", 2},
	{"%s announces $2B buyback", "The board authorized repurchases of up to 8% of shares outstanding, effective immediately.", 1},
	{"Analysts lift %s to Overweight", "The note cites \"a pipeline the market is pricing at zero\" and puts the target 20% above the tape.", 1},
	{"%s prototype clears certification a full quarter early", "Regulators signed off after an unusually clean review cycle; commercial shipments can begin at once.", 2},
	{"%s lands strategic partnership with sovereign wealth fund", "The deal includes an equity stake at a premium to market and a decade of committed offtake.", 3},
	{"Short seller capitulates on %s position", "The fund disclosed it has closed its short \"with regret and a diminished bonus pool\".", 1},
	{"%s granted key patent after seven-year dispute", "The ruling is final and unappealable; competitors must license or redesign.", 2},
	{"Activist investor discloses 6% stake in %s", "The letter to the board is short, polite, and unmistakably a threat.", 2},
	{"%s ships flagship product six weeks ahead of schedule", "Early channel checks suggest sell-through is running hot in every region.", 1},
}

var negative = []template{
	{"%s cuts guidance on softening demand", "Management blamed \"macro conditions\"; analysts on the call blamed management.", -2},
	{"Regulators open probe into %s accounting", "The inquiry concerns revenue recognition in the services segment. The company says it is cooperating fully.", -3},
	{"%s hit by multi-region service outage", "The incident began during a routine deployment and is now in its third hour. Enterprise customers are vocal.", -2},
	{"%s CFO departs \"to pursue other opportunities\"", "The search for a successor begins immediately; the timing, mid-quarter, raised eyebrows.", -2},
	{"Analysts cut %s to Underweight", "The downgrade cites channel inventory \"two standard deviations above comfortable\".", -1},
	{"%s recalls flagship unit over thermal fault", "The recall covers roughly 12% of units shipped this year. No injuries reported.", -2},
	{"Short report targets %s: \"a lattice of related-party transactions\"", "The company called the report \"malicious fiction\" and threatened litigation before lunch.", -3},
	{"%s loses appeal in patent dispute", "Damages will be set next quarter; injunction risk is the larger overhang.", -2},
	{"Key customer diversifies away from %s", "The contract renewal came in at half the prior volume commitment.", -1},
	{"%s delays product launch citing supply constraints", "A single supplier of a single component, once again, holds the roadmap hostage.", -1},
}

var macro = []template{
	{"Inflation print comes in hot; rate path repriced", "The tape's morning thesis did not survive contact with the data.", -2},
	{"Central bank signals earlier easing than expected", "Risk assets celebrated before the sentence was finished.", 2},
	{"Liquidity thins as clearing house raises margin requirements", "Desks are trimming gross exposure into the close.", -1},
	{"Sovereign fund announces broad equity allocation increase", "The mandate is passive, patient, and extremely large.", 2},
	{"Geopolitical flashpoint rattles risk appetite", "Correlations, as always in a storm, went to one.", -2},
	{"Index rebalance triggers heavy closing flows", "Passive money moves nothing until it moves everything.", 1},
	{"Exchange-wide volatility surge as leveraged funds unwind", "Someone, somewhere, got a phone call they had been dreading.", -3},
	{"Soft landing chatter returns; breadth improves", "Strategists who were bearish yesterday were \"constructive all along\".", 1},
}

// Engine schedules and publishes news.
type Engine struct {
	mu     sync.Mutex
	items  []Item
	nextID int64

	agents    map[string]*sim.Agent
	names     map[string]string // symbol -> company name
	rng       *rand.Rand
	broadcast func(Item)
}

const maxItems = 300

func New(agents map[string]*sim.Agent, names map[string]string, seed int64, broadcast func(Item)) *Engine {
	return &Engine{
		agents:    agents,
		names:     names,
		rng:       rand.New(rand.NewSource(seed)),
		broadcast: broadcast,
	}
}

// Run emits stories forever: first one quickly (demos matter), then on an
// exponential clock averaging ~40s.
func (n *Engine) Run(ctx context.Context) {
	wait := 12 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			n.publish()
			wait = time.Duration(15+n.rng.ExpFloat64()*40) * time.Second
			if wait > 3*time.Minute {
				wait = 3 * time.Minute
			}
		}
	}
}

func (n *Engine) publish() {
	n.mu.Lock()
	defer n.mu.Unlock()

	isMacro := n.rng.Float64() < 0.18
	var t template
	var symbol, name string

	if isMacro {
		t = macro[n.rng.Intn(len(macro))]
	} else {
		symbols := make([]string, 0, len(n.agents))
		for s := range n.agents {
			symbols = append(symbols, s)
		}
		if len(symbols) == 0 {
			return
		}
		symbol = symbols[n.rng.Intn(len(symbols))]
		name = n.names[symbol]
		if n.rng.Float64() < 0.5 {
			t = positive[n.rng.Intn(len(positive))]
		} else {
			t = negative[n.rng.Intn(len(negative))]
		}
	}

	impact := n.impactFor(t.sev)
	item := Item{
		ID: n.nextIDLocked(), TS: time.Now().UnixMilli(),
		Symbol: symbol, Severity: t.sev, ImpactPct: impact,
	}
	if isMacro {
		item.Headline = t.head
		item.Body = t.body
	} else {
		item.Headline = fmt.Sprintf(t.head, name)
		item.Body = t.body
	}

	shock := sim.Shock{
		JumpFrac: impact / 100,
		VolMult:  1 + float64(abs(t.sev))*(1.0+n.rng.Float64()),
		Duration: time.Duration(20+n.rng.Intn(55)) * time.Second,
	}
	if isMacro {
		// macro hits everything, but each name idiosyncratically
		for _, a := range n.agents {
			s := shock
			s.JumpFrac *= 0.4 + 0.6*n.rng.Float64()
			a.ApplyShock(s)
		}
	} else if a := n.agents[symbol]; a != nil {
		a.ApplyShock(shock)
	}

	n.items = append(n.items, item)
	if len(n.items) > maxItems {
		n.items = n.items[len(n.items)-maxItems:]
	}
	if n.broadcast != nil {
		n.broadcast(item)
	}
}

// impactFor maps severity to a signed percentage jump. Severity-3 stories
// can gap hard enough to trip the volatility circuit breaker — by design.
func (n *Engine) impactFor(sev int) float64 {
	mag := float64(abs(sev)) * (0.35 + 0.55*n.rng.Float64())
	if abs(sev) == 3 {
		mag = 2.5 + 3.0*n.rng.Float64()
	}
	if sev < 0 {
		return -mag
	}
	return mag
}

func (n *Engine) nextIDLocked() int64 {
	n.nextID++
	return n.nextID
}

// Items returns the most recent stories, newest first.
func (n *Engine) Items(limit int) []Item {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Item, 0, limit)
	for i := len(n.items) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, n.items[i])
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
