package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// shutdownLimit is how long a stop waits for the requests still running. Every long-lived
// answer ends on its request's context, so a stop takes milliseconds; the limit only matters for
// a handler that does not (B-151).
const shutdownLimit = 5 * time.Second

// idleTimeout closes a keep-alive connection without a request (B-232); a variable for the test.
var idleTimeout = 120 * time.Second

// newHTTPServer is occulited's HTTP server with a base context that endRequests cancels: every
// request's context derives from it, so the long-lived answers (the change stream, the
// service-message stream, the Log page's follow, lite-rpc's event stream) return at the stop,
// and Shutdown does not wait out its limit for them (B-151).
//
// The timeouts (B-232): a request's header within 10 s, and a keep-alive connection that carries
// no request for idleTimeout is closed (lighttpd keeps its own for 5 s, so nothing legitimate
// idles that long). No ReadTimeout - a backup restore streams its upload for minutes - and no
// WriteTimeout - the streams write for hours; neither is an idle connection, so IdleTimeout
// leaves them alone.
func newHTTPServer(addr string, h http.Handler) (srv *http.Server, endRequests context.CancelFunc) {
	reqCtx, endRequests := context.WithCancel(context.Background())
	srv = &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: idleTimeout,
		BaseContext: func(net.Listener) context.Context { return reqCtx }}
	return srv, endRequests
}

// stopHTTPServer ends the requests, then shuts the server down. A stop is not a failure: a
// connection still open after the limit is closed with a warning, and the caller exits 0, so the
// unit ends as deactivated rather than failed (exit 1 on "context deadline exceeded" before).
func stopHTTPServer(srv *http.Server, endRequests context.CancelFunc, limit time.Duration, log *slog.Logger) {
	endRequests()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Warn("occulited stopping: connections still open were closed", "err", err)
		_ = srv.Close()
	}
}
