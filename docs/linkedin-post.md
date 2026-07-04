# LinkedIn post draft

> Paste-ready. Attach a 30–60s screen recording of the terminal (see shot list below) —
> posts with a demo video massively outperform text-only. Swap the repo link once pushed.

---

I built a stock exchange from scratch. Then I gave it a news cycle.

**Meridian** is a fully self-contained financial exchange I wrote to sharpen my systems
engineering: a matching engine in Go, a living simulated market, and a Bloomberg-style
trading terminal in React/TypeScript.

What's inside:

🔧 A price-time priority matching engine — one lock-free goroutine per instrument
(LMAX-style single writer), integer-only matching path, ~2.2M ops/sec at 458ns per
operation on a laptop.

📰 A news engine that writes fictional headlines ("Short report targets Helix Dynamics")
and injects the corresponding price jump and volatility regime into the market — so
charts don't just wiggle, things *happen*.

⛔ Volatility circuit breakers: a 4% move in 30 seconds halts the stock with a live
countdown, just like a real venue.

🤖 One-click deployable trading bots — momentum, mean-reversion, market-making — each
with its own $250k book and the same risk checks as a human. A live leaderboard shows
whether you, your bots, or the house algos are winning.

📡 Real exchange plumbing: event-sourced journal with deterministic replay, WebSocket
market data as snapshot + sequenced deltas with gap recovery, explicit backpressure
(slow consumers get dropped, the matching path never blocks on I/O).

No database, no external APIs. Clone, run two commands, trade.

The most fun part: deploying a momentum bot and a mean-reversion bot on the same stock
and watching the news regime decide which one eats the other.

Repo: github.com/alim596/meridian
Stack: Go · React · TypeScript · WebSockets · zero frameworks on the matching path

#golang #typescript #systemsengineering #fintech #softwareengineering #buildinpublic

---

## Shot list for the demo video

1. Full terminal, market moving (3–4s hold).
2. A headline landing in the Wire panel → chart gaps, tape accelerates.
3. A HALT: banner pulsing on the book, countdown, resume.
4. Deploy a momentum bot in the Algo Deck → its P&L starts printing.
5. Leaderboard tab: you vs. bots vs. house.
6. Terminal window beside `go test ./...` and the replay CLI output ("sequence
   integrity OK") for the engineering crowd.
