// Package server exposes the exchange over HTTP (order entry, account,
// reference data) and WebSocket (market data). Routing is stdlib net/http —
// Go 1.22 pattern matching makes a router dependency unnecessary.
package server

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/alim596/meridian/internal/account"
	"github.com/alim596/meridian/internal/engine"
	"github.com/alim596/meridian/internal/marketdata"
	"github.com/alim596/meridian/internal/orderbook"
)

const depthLevels = 24

type Server struct {
	engines map[string]*engine.Engine
	order   []string // stable instrument ordering for listings
	acct    *account.Manager
	md      *marketdata.MarketData
	hub     *Hub
	started time.Time
}

func New(engines map[string]*engine.Engine, order []string, acct *account.Manager, md *marketdata.MarketData, hub *Hub) *Server {
	return &Server{
		engines: engines, order: order, acct: acct, md: md, hub: hub,
		started: time.Now(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ws/market", s.hub.handleWS)

	mux.HandleFunc("POST /api/session", s.createSession)
	mux.HandleFunc("GET /api/instruments", s.listInstruments)
	mux.HandleFunc("GET /api/depth", s.getDepth)
	mux.HandleFunc("GET /api/candles", s.getCandles)
	mux.HandleFunc("GET /api/metrics", s.getMetrics)

	mux.HandleFunc("POST /api/orders", s.auth(s.placeOrder))
	mux.HandleFunc("DELETE /api/orders/{instrument}/{id}", s.auth(s.cancelOrder))
	mux.HandleFunc("GET /api/orders", s.auth(s.openOrders))
	mux.HandleFunc("GET /api/account", s.auth(s.getAccount))
	mux.HandleFunc("GET /api/fills", s.auth(s.getFills))

	return cors(mux)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type authedHandler func(w http.ResponseWriter, r *http.Request, accountID string)

func (s *Server) auth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		id, ok := s.acct.Resolve(key)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		next(w, r, id)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ---- public endpoints ----

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&body) // body optional
	id, key := s.acct.CreateSession(body.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"accountId": id,
		"apiKey":    key,
		"cash":      account.StartingCash,
	})
}

type instrumentInfo struct {
	engine.Instrument
	Stats marketdata.Stats `json:"stats"`
}

func (s *Server) listInstruments(w http.ResponseWriter, _ *http.Request) {
	out := make([]instrumentInfo, 0, len(s.order))
	for _, sym := range s.order {
		eng := s.engines[sym]
		st, _ := s.md.StatsFor(sym)
		out = append(out, instrumentInfo{Instrument: eng.Inst, Stats: st})
	}
	writeJSON(w, http.StatusOK, out)
}

func packLevels(levels []orderbook.PriceLevel) [][2]int64 {
	out := make([][2]int64, len(levels))
	for i, l := range levels {
		out[i] = [2]int64{l.Price, l.Qty}
	}
	return out
}

func (s *Server) getDepth(w http.ResponseWriter, r *http.Request) {
	eng, ok := s.engines[r.URL.Query().Get("instrument")]
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown instrument")
		return
	}
	levels := depthLevels
	if n, err := strconv.Atoi(r.URL.Query().Get("levels")); err == nil && n > 0 && n <= 100 {
		levels = n
	}
	snap := eng.Snapshot(levels)
	writeJSON(w, http.StatusOK, map[string]any{
		"seq":  snap.Seq,
		"bids": packLevels(snap.Bids),
		"asks": packLevels(snap.Asks),
		"last": snap.LastPrice,
	})
}

func (s *Server) getCandles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym := q.Get("instrument")
	if _, ok := s.engines[sym]; !ok {
		writeErr(w, http.StatusNotFound, "unknown instrument")
		return
	}
	interval := q.Get("interval")
	if _, ok := marketdata.IntervalByName(interval); !ok {
		writeErr(w, http.StatusBadRequest, "interval must be one of 1s, 5s, 1m")
		return
	}
	limit := 500
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 3000 {
		limit = n
	}
	writeJSON(w, http.StatusOK, s.md.Candles(sym, interval, limit))
}

func (s *Server) getMetrics(w http.ResponseWriter, _ *http.Request) {
	type instMetrics struct {
		Symbol  string `json:"symbol"`
		Orders  int64  `json:"orders"`
		Latency any    `json:"latency"`
	}
	insts := make([]instMetrics, 0, len(s.order))
	for _, sym := range s.order {
		snap := s.engines[sym].Latency.Snapshot()
		insts = append(insts, instMetrics{Symbol: sym, Orders: snap.Count, Latency: snap})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptimeSec":   int64(time.Since(s.started).Seconds()),
		"goroutines":  runtime.NumGoroutine(),
		"instruments": insts,
	})
}

// ---- authenticated endpoints ----

type orderRequest struct {
	Instrument string `json:"instrument"`
	Side       string `json:"side"` // buy | sell
	Type       string `json:"type"` // limit | market
	TIF        string `json:"tif"`  // gtc | ioc (default gtc; market forces ioc)
	Price      int64  `json:"price"`
	Qty        int64  `json:"qty"`
}

func (s *Server) placeOrder(w http.ResponseWriter, r *http.Request, accountID string) {
	var req orderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	eng, ok := s.engines[req.Instrument]
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown instrument")
		return
	}

	var side orderbook.Side
	switch req.Side {
	case "buy":
		side = orderbook.Bid
	case "sell":
		side = orderbook.Ask
	default:
		writeErr(w, http.StatusBadRequest, "side must be buy or sell")
		return
	}

	typ := orderbook.Limit
	tif := orderbook.GTC
	switch req.Type {
	case "limit", "":
	case "market":
		typ = orderbook.Market
		tif = orderbook.IOC
	default:
		writeErr(w, http.StatusBadRequest, "type must be limit or market")
		return
	}
	if req.TIF == "ioc" {
		tif = orderbook.IOC
	}

	resp := make(chan engine.SubmitResp, 1)
	eng.Submit(engine.SubmitCmd{
		Account: accountID, Side: side, Type: typ, TIF: tif,
		Price: req.Price, Qty: req.Qty, Resp: resp,
	})
	result := <-resp
	code := http.StatusOK
	if result.Status == "rejected" {
		code = http.StatusUnprocessableEntity
	}
	writeJSON(w, code, result)
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request, accountID string) {
	eng, ok := s.engines[r.PathValue("instrument")]
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown instrument")
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid order id")
		return
	}
	resp := make(chan engine.CancelResp, 1)
	eng.Cancel(engine.CancelCmd{OrderID: id, Account: accountID, Resp: resp})
	result := <-resp
	if !result.OK {
		writeErr(w, http.StatusNotFound, result.Reason)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) openOrders(w http.ResponseWriter, _ *http.Request, accountID string) {
	orders := s.acct.OpenOrders(accountID)
	if orders == nil {
		orders = []account.OpenOrder{}
	}
	writeJSON(w, http.StatusOK, orders)
}

func (s *Server) getAccount(w http.ResponseWriter, _ *http.Request, accountID string) {
	view, ok := s.acct.Snapshot(accountID, s.md.Marks())
	if !ok {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) getFills(w http.ResponseWriter, r *http.Request, accountID string) {
	since := uint64(0)
	if n, err := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64); err == nil {
		since = n
	}
	writeJSON(w, http.StatusOK, s.acct.Fills(accountID, since))
}
