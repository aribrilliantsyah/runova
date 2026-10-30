package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"runova/internal/auth"
	"runova/internal/docker"
)

const (
	defaultLogTail = 200
	maxLogTail     = 5000
	wsWriteTimeout = 10 * time.Second
)

// acceptWebSocket upgrades the request. The origin must match the request
// host; the session cookie was already checked by the route's auth wrapper.
func acceptWebSocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{r.Host}})
}

// sessionRecheck is how often an open WebSocket re-validates the session that
// opened it.
var sessionRecheck = 30 * time.Second

// watchSession calls gone once the request's session no longer belongs to the
// user who opened the stream: logout, password change and user deletion all
// delete the session (spec §3), and a stream opened before must not outlive it.
// It returns when ctx ends.
func (s *server) watchSession(ctx context.Context, r *http.Request, owner int64, gone func()) {
	t := time.NewTicker(sessionRecheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			u, ok, err := s.sessions.User(r)
			if err == nil && (!ok || u.ID != owner) {
				gone()
				return
			}
		}
	}
}

// logs streams the app's container log as JSON docker.LogLine messages, then
// follows it until the client leaves.
func (s *server) logs(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	tail := defaultLogTail
	if v := r.URL.Query().Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxLogTail {
			writeError(w, http.StatusBadRequest, "Parameter tail harus antara 0 dan "+strconv.Itoa(maxLogTail))
			return
		}
		tail = n
	}

	// Errors surface as plain HTTP responses: nothing is upgraded yet.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	rc, err := s.apps.Logs(ctx, u, id, tail, true)
	if err != nil {
		appError(w, r, err)
		return
	}
	defer rc.Close()

	conn, err := acceptWebSocket(w, r)
	if err != nil {
		return // Accept already wrote the response
	}
	defer conn.CloseNow()

	// Reading is what lets the library notice the client closing.
	closed := conn.CloseRead(ctx)
	go s.watchSession(ctx, r, u.ID, func() {
		conn.Close(websocket.StatusPolicyViolation, "Sesi berakhir")
		cancel()
	})
	go func() {
		<-closed.Done()
		cancel()
		rc.Close()
	}()

	err = docker.ReadLogs(rc, func(l docker.LogLine) error {
		wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer wcancel()
		return wsjson.Write(wctx, conn, l)
	})
	switch {
	case ctx.Err() != nil:
		conn.Close(websocket.StatusNormalClosure, "")
	case err != nil && !errors.Is(err, context.Canceled):
		slog.Debug("log stream ended", "err", err)
		conn.Close(websocket.StatusInternalError, "log stream failed")
	default:
		conn.Close(websocket.StatusNormalClosure, "container stopped")
	}
}
