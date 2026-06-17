package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/keepdevops/cofiswarm-dispatch/internal/buspresence"
	"github.com/keepdevops/cofiswarm-dispatch/internal/history"
	"github.com/keepdevops/cofiswarm-dispatch/internal/httpapi"
	"github.com/keepdevops/cofiswarm-dispatch/internal/session"
)

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
	var alerter httpapi.Alerter
	if base := os.Getenv("COFISWARM_BRIDGE_URL"); base != "" {
		pub := buspresence.New(base)
		pub.Announce()
		go pub.WatchHello(context.Background())
		alerter = pub
		log.Printf("dispatch publishing presence/alerts to bus via %s", base)
	}

	log.Printf("dispatch listening on %s (sessions=%d history=%d)", *addr, sessions.Count(), hist.Len())
	log.Fatal(http.ListenAndServe(*addr, httpapi.New(sessions, hist, alerter).Handler()))
}
