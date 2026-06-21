package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/keepdevops/cofiswarm-dispatch/internal/history"
	"github.com/keepdevops/cofiswarm-dispatch/internal/httpapi"
	"github.com/keepdevops/cofiswarm-dispatch/internal/session"
	"github.com/keepdevops/cofiswarm-observer-sdk/pkg/buspresence"
)

// alertFunc adapts a component-bound alert closure to httpapi.Alerter, since the shared SDK's
// Publisher.Alert takes (componentID, message) while httpapi only knows about the message.
type alertFunc func(string)

func (f alertFunc) Alert(msg string) { f(msg) }

func main() {
	addr := flag.String("listen", ":8010", "listen address")
	state := flag.String("state", "", "dispatch state root")
	flag.Parse()
	if *state == "" {
		if v := os.Getenv("COFISWARM_VAR_LIB"); v != "" {
			*state = filepath.Join(v, "cofiswarm", "dispatch")
		} else {
			*state = "/var/lib/cofiswarm/dispatch"
		}
	}
	sessPath := filepath.Join(*state, "sessions", "sessions.json")
	histPath := filepath.Join(*state, "history", "history.json")
	sessions, err := session.New(sessPath)
	if err != nil {
		log.Fatal(err)
	}
	hist, err := history.New(histPath)
	if err != nil {
		log.Fatal(err)
	}
	// Optional: join the observer bus (default-off). Announces dispatch presence and
	// publishes dependency alerts when a mode relay is unavailable.
	const dispatchID = "dispatch"
	dispatchInfo := map[string]any{"name": dispatchID, "engine": "orchestrator"}
	var pub *buspresence.Publisher
	var alerter httpapi.Alerter
	stopWatch := func() {}
	if base := os.Getenv("COFISWARM_BRIDGE_URL"); base != "" {
		pub = buspresence.New(base)
		announce := func() { pub.Announce(dispatchID, dispatchInfo) }
		announce()
		var wctx context.Context
		wctx, stopWatch = context.WithCancel(context.Background())
		go pub.WatchHello(wctx, announce)
		alerter = alertFunc(func(msg string) { pub.Alert(dispatchID, msg) })
		log.Printf("dispatch publishing presence/alerts to bus via %s", base)
	}

	srv := &http.Server{Addr: *addr, Handler: httpapi.New(sessions, hist, alerter).Handler()}
	go func() {
		log.Printf("dispatch listening on %s (sessions=%d history=%d)", *addr, sessions.Count(), hist.Len())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("dispatch: server error: %v", err)
		}
	}()

	// On SIGINT/SIGTERM: stop re-announcing, say goodbye (flip offline now, not after the
	// observer's TTL), then drain in-flight requests before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Printf("dispatch: shutting down")
	stopWatch()
	if pub != nil {
		pub.Goodbye(dispatchID)
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("dispatch: graceful shutdown: %v", err)
	}
}
