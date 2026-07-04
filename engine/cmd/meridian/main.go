// Command meridian runs the exchange: one matching engine per instrument,
// the market simulation, the event journal, and the HTTP/WebSocket API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/alim596/meridian/internal/account"
	"github.com/alim596/meridian/internal/bus"
	"github.com/alim596/meridian/internal/engine"
	"github.com/alim596/meridian/internal/journal"
	"github.com/alim596/meridian/internal/marketdata"
	"github.com/alim596/meridian/internal/server"
	"github.com/alim596/meridian/internal/sim"
)

// Fictional instruments; prices in ticks of $0.01.
var instruments = []engine.Instrument{
	{Symbol: "NVR", Name: "Novera Systems", PriceScale: 100, InitPrice: 18450},
	{Symbol: "HLX", Name: "Helix Dynamics", PriceScale: 100, InitPrice: 9230},
	{Symbol: "ARC", Name: "Arclight Energy", PriceScale: 100, InitPrice: 4175},
	{Symbol: "QTM", Name: "Quanta Materials", PriceScale: 100, InitPrice: 26710},
	{Symbol: "VYR", Name: "Veyron Logistics", PriceScale: 100, InitPrice: 5890},
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data", "data", "journal directory")
	simOn := flag.Bool("sim", true, "run the market simulation")
	seed := flag.Int64("seed", time.Now().UnixNano(), "simulation RNG seed")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	acct := account.NewManager()
	md := marketdata.New()
	jnl, err := journal.New(*dataDir)
	if err != nil {
		log.Fatalf("journal: %v", err)
	}

	evBus := bus.New(16384)
	engines := make(map[string]*engine.Engine, len(instruments))
	order := make([]string, 0, len(instruments))
	for _, inst := range instruments {
		engines[inst.Symbol] = engine.New(inst, acct, evBus.Publish)
		order = append(order, inst.Symbol)
		md.Seed(inst.Symbol, inst.InitPrice)
	}

	hub := server.NewHub(engines)
	evBus.Subscribe(jnl.OnEvent)
	evBus.Subscribe(acct.OnEvent)
	evBus.Subscribe(md.OnEvent)
	evBus.Subscribe(hub.OnEvent)

	go evBus.Run(ctx)
	go jnl.Run(ctx)
	for _, eng := range engines {
		go eng.Run(ctx)
	}
	go hub.RunStatsTicker(ctx, func() any { return md.AllStats() })

	if *simOn {
		for i, inst := range instruments {
			go sim.Run(ctx, engines[inst.Symbol], acct, *seed+int64(i))
		}
		log.Printf("simulation running for %d instruments (seed %d)", len(instruments), *seed)
	}

	srv := &http.Server{Addr: *addr, Handler: server.New(engines, order, acct, md, hub).Handler()}
	go func() {
		log.Printf("meridian exchange listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	fmt.Println()
	log.Println("shutting down: draining journal and closing connections")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	jnl.Close()
	log.Println("goodbye")
}
