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
	"github.com/alim596/meridian/internal/bots"
	"github.com/alim596/meridian/internal/engine"
	"github.com/alim596/meridian/internal/marketdata"
	"github.com/alim596/meridian/internal/news"
	"github.com/alim596/meridian/internal/orderbook"
)

const depthLevels = 24

// Deps bundles everything the API serves.
type Deps struct {
	Engines map[string]*engine.Engine
	Order   []string // stable instrument ordering for listings
	Acct    *account.Manager
	MD      *marketdata.MarketData
	Hub     *Hub
	News    *news.Engine
	Bots    *bots.Manager
}

type Server struct {
	Deps
	started time.Time
}

func New(d Deps) *Server {
	return &Server{Deps: d, started: time.Now()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ws/market", s.Hub.handleWS)

	mux.HandleFunc("POST /api/session", s.createSession)
	mux.HandleFunc("GET /api/instruments", s.listInstruments)
	mux.HandleFunc("GET /api/depth", s.getDepth)
	mux.HandleFunc("GET /api/candles", s.getCandles)
	mux.HandleFunc("GET /api/metrics", s.getMetrics)

	mux.HandleFunc("GET /api/news", s.getNews)
	mux.HandleFunc("GET /api/leaderboard", s.getLeaderboard)

	mux.HandleFunc("POST /api/orders", s.auth(s.placeOrder))
	mux.HandleFunc("DELETE /api/orders/{instrument}/{id}", s.auth(s.cancelOrder))
	mux.HandleFunc("GET /api/orders", s.auth(s.openOrders))
	mux.HandleFunc("GET /api/account", s.auth(s.getAccount))
	mux.HandleFunc("PATCH /api/account", s.auth(s.renameAccount))
	mux.HandleFunc("GET /api/fills", s.auth(s.getFills))
	mux.HandleFunc("POST /api/bots", s.auth(s.deployBot))
	mux.HandleFunc("GET /api/bots", s.auth(s.listBots))
	mux.HandleFunc("DELETE /api/bots/{id}", s.auth(s.stopBot))

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
		id, ok := s.Acct.Resolve(key)
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
	id, key := s.Acct.CreateSession(body.Name)
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
	out := make([]instrumentInfo, 0, len(s.Order))
	for _, sym := range s.Order {
		eng := s.Engines[sym]
		st, _ := s.MD.StatsFor(sym)
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
	eng, ok := s.Engines[r.URL.Query().Get("instrument")]
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
	if _, ok := s.Engines[sym]; !ok {
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
	writeJSON(w, http.StatusOK, s.MD.Candles(sym, interval, limit))
}

func (s *Server) getMetrics(w http.ResponseWriter, _ *http.Request) {
	type instMetrics struct {
		Symbol  string `json:"symbol"`
		Orders  int64  `json:"orders"`
		Latency any    `json:"latency"`
	}
	insts := make([]instMetrics, 0, len(s.Order))
	for _, sym := range s.Order {
		snap := s.Engines[sym].Latency.Snapshot()
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
	eng, ok := s.Engines[req.Instrument]
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
	eng, ok := s.Engines[r.PathValue("instrument")]
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
	orders := s.Acct.OpenOrders(accountID)
	if orders == nil {
		orders = []account.OpenOrder{}
	}
	writeJSON(w, http.StatusOK, orders)
}

func (s *Server) getAccount(w http.ResponseWriter, _ *http.Request, accountID string) {
	view, ok := s.Acct.Snapshot(accountID, s.MD.Marks())
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
	writeJSON(w, http.StatusOK, s.Acct.Fills(accountID, since))
}

// ---- v2: news, leaderboard, bots ----

func (s *Server) getNews(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 300 {
		limit = n
	}
	writeJSON(w, http.StatusOK, s.News.Items(limit))
}

func (s *Server) getLeaderboard(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Acct.Leaderboard(s.MD.Marks(), 25))
}

func (s *Server) renameAccount(w http.ResponseWriter, r *http.Request, accountID string) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	if !s.Acct.Rename(accountID, body.Name) {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": body.Name})
}

func (s *Server) deployBot(w http.ResponseWriter, r *http.Request, accountID string) {
	var body struct {
		Instrument string `json:"instrument"`
		Strategy   string `json:"strategy"`
		Name       string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	bot, err := s.Bots.Deploy(accountID, body.Instrument, body.Strategy, body.Name)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, bot)
}

func (s *Server) listBots(w http.ResponseWriter, _ *http.Request, accountID string) {
	writeJSON(w, http.StatusOK, s.Bots.List(accountID, s.MD.Marks()))
}

func (s *Server) stopBot(w http.ResponseWriter, r *http.Request, accountID string) {
	if err := s.Bots.Stop(accountID, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}
