package demo

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

// Store is a stand-in for whatever this fictional service persists to.
type Store struct{}

func (s *Store) Ping() error         { return nil }
func (s *Store) PruneExpired() error { return nil }

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

// run is deliberately a little branchy — a handful of guard clauses and a
// select statement — so it's a realistic stand-in for "the call site is
// already busy."
func run() error {
	env := os.Getenv("ENV")
	if env == "" {
		return errors.New("ENV is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		return errors.New("PORT is required")
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return err
	}
	if n < 1 || n > 65535 {
		return errors.New("invalid port")
	}
	if n < 1024 && env == "production" {
		return errors.New("production must not bind a privileged port")
	}

	store := &Store{}
	if err := store.Ping(); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(store)) // handler factory: this IS a call
	mux.HandleFunc("/login", handleLogin)           // bare reference: this is NOT a call
	go sweepExpired(context.Background(), store)

	srv := buildServer(env, mux)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case err := <-errCh:
		return err
	case <-sig:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if env == "production" {
		if os.Getenv("STRICT") == "" {
			return errors.New("STRICT must be set in production")
		}
	}
	return nil
}

// buildServer is a simple, single-branch constructor — a realistic stand-in
// for "the call site is not already busy."
func buildServer(env string, mux *http.ServeMux) *http.Server {
	return &http.Server{
		Addr:        ":8080",
		Handler:     mux,
		ReadTimeout: newTimeoutSeconds(env),
	}
}

// newTimeoutSeconds has no independent meaning beyond the one if/else it
// wraps. This is the "just inline it" case.
func newTimeoutSeconds(env string) time.Duration {
	if env == "production" {
		return 30 * time.Second
	}
	return 5 * time.Second
}

// healthHandler is a handler *factory* — called once, directly, to produce
// the closure the mux actually serves. That direct call is what makes it a
// candidate at all; a route registered by bare reference (handleLogin,
// below) never is.
func healthHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.Ping(); err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// handleLogin is registered as a bare value — mux.HandleFunc("/login",
// handleLogin) — so it is never "called" in any sense singlecaller can
// point at, no matter how many times someone visits the route.
func handleLogin(w http.ResponseWriter, r *http.Request) {
	redirect := sanitizeRedirectPath(r.URL.Query().Get("redirect"))
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// sanitizeRedirectPath is a named, single-purpose open-redirect guard. Short
// and single-caller, same as newTimeoutSeconds above — but this one is
// worth keeping separate regardless of caller count, because burying a
// security check inline makes it harder to audit later.
func sanitizeRedirectPath(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return "/"
	}
	return p
}

// sweepExpired is a background loop launched with go sweepExpired(...).
// Its one caller is a `go` statement, which still counts as a call — but
// inlining a ticker loop into whatever launches it wouldn't help anyone.
func sweepExpired(ctx context.Context, store *Store) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			store.PruneExpired()
		}
	}
}
