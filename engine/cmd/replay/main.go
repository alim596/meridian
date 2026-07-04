// Command replay reconstructs an instrument's full order-book state from its
// event journal and verifies stream integrity — the event-sourcing payoff:
// the journal alone is enough to audit every fill and rebuild the book.
//
//	go run ./cmd/replay -file data/journal-NVR.jsonl
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/alim596/meridian/internal/engine"
	"github.com/alim596/meridian/internal/journal"
)

func main() {
	file := flag.String("file", "", "path to a journal-<SYMBOL>.jsonl file")
	depth := flag.Int("depth", 5, "book levels to print per side")
	flag.Parse()
	if *file == "" {
		flag.Usage()
		os.Exit(2)
	}

	// Rebuild L2 state by folding the level updates; verify seq continuity.
	bids := map[int64]int64{}
	asks := map[int64]int64{}
	var (
		lastSeq             uint64
		gaps                int64
		events, trades      int64
		volume, notional    int64
		accepted, canceled  int64
		rejected            int64
		lastPrice           int64
		instrument          string
	)

	err := journal.Read(*file, func(ev engine.Event) error {
		events++
		instrument = ev.Instrument
		if lastSeq != 0 && ev.Seq != lastSeq+1 {
			gaps++
		}
		lastSeq = ev.Seq
		switch ev.Kind {
		case engine.EvTrade:
			trades++
			volume += ev.Trade.Qty
			notional += ev.Trade.Qty * ev.Trade.Price
			lastPrice = ev.Trade.Price
		case engine.EvAccepted:
			accepted++
		case engine.EvCanceled:
			canceled++
		case engine.EvRejected:
			rejected++
		case engine.EvL2:
			m := bids
			if ev.Level.Side == "sell" {
				m = asks
			}
			if ev.Level.Qty == 0 {
				delete(m, ev.Level.Price)
			} else {
				m[ev.Level.Price] = ev.Level.Qty
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "replay: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("journal        %s\n", *file)
	fmt.Printf("instrument     %s\n", instrument)
	fmt.Printf("events         %d (final seq %d, gaps %d)\n", events, lastSeq, gaps)
	fmt.Printf("orders         %d accepted / %d canceled / %d rejected\n", accepted, canceled, rejected)
	fmt.Printf("trades         %d (volume %d lots, notional %d ticks)\n", trades, volume, notional)
	if trades > 0 {
		fmt.Printf("vwap           %.2f ticks (last %d)\n", float64(notional)/float64(volume), lastPrice)
	}

	fmt.Printf("\nreconstructed book (top %d):\n", *depth)
	fmt.Printf("%12s %10s | %-10s %-12s\n", "BID QTY", "BID", "ASK", "ASK QTY")
	bp := sortedKeys(bids, true)
	ap := sortedKeys(asks, false)
	for i := 0; i < *depth; i++ {
		var l, r string
		if i < len(bp) {
			l = fmt.Sprintf("%12d %10d", bids[bp[i]], bp[i])
		} else {
			l = fmt.Sprintf("%12s %10s", "", "")
		}
		if i < len(ap) {
			r = fmt.Sprintf("%-10d %-12d", ap[i], asks[ap[i]])
		}
		fmt.Printf("%s | %s\n", l, r)
	}
	if gaps > 0 {
		fmt.Fprintln(os.Stderr, "\nWARNING: sequence gaps detected — journal is incomplete")
		os.Exit(1)
	}
	fmt.Println("\nsequence integrity OK — every event accounted for")
}

func sortedKeys(m map[int64]int64, desc bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if desc {
			return out[i] > out[j]
		}
		return out[i] < out[j]
	})
	return out
}
