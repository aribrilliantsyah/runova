package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"runova/internal/auth"
	"runova/internal/store"
)

const (
	defaultTerminalIdle = 30 * time.Minute
	terminalChunk       = 32 << 10
	maxResize           = 1000
)

// terminalControl is a text message from the browser; binary messages are stdin.
type terminalControl struct {
	Type string `json:"type"`
	Rows uint   `json:"rows"`
	Cols uint   `json:"cols"`
}

// terminal attaches the browser to /bin/sh in the app's container. Binary
// WebSocket messages carry stdin and stdout; text messages are JSON control
// messages ({"type":"resize","rows":24,"cols":80}). The session ends when
// either side closes or after the idle timeout without any traffic.
func (s *server) terminal(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	// Everything that can be refused is refused before the upgrade.
	// Spec §5: the terminal is only for the user's own containers. Admins
	// may read logs of any app, but a shell runs with the owner's UID inside
	// the owner's container and is not part of that access.
	app, _, err := s.apps.Get(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	if app.UserID != u.ID {
		writeError(w, http.StatusNotFound, appNotFound)
		return
	}
	if err := s.apps.CheckTerminal(r.Context(), u, id); err != nil {
		appError(w, r, err)
		return
	}
	sess, _, err := s.apps.Terminal(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	defer sess.Close()

	conn, err := acceptWebSocket(w, r)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(terminalChunk)
	s.record(r, u, store.ActTerminal, app.Name, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go s.watchSession(ctx, r, u.ID, func() { conn.Close(websocket.StatusPolicyViolation, "Sesi berakhir") })
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	touch := func() { last.Store(time.Now().UnixNano()) }

	// Idle watchdog: any message in either direction counts as activity. It
	// sends the close frame itself; reads use the request context because
	// canceling a read's context would drop the connection before that frame.
	var idled atomic.Bool
	idle := make(chan struct{})
	go func() {
		defer close(idle)
		t := time.NewTicker(max(s.idle/10, time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, last.Load())) >= s.idle {
					idled.Store(true)
					conn.Close(websocket.StatusGoingAway, "Sesi ditutup karena tidak aktif")
					return
				}
			}
		}
	}()

	done := make(chan struct{}, 2)
	go func() { // shell → browser
		defer func() { done <- struct{}{} }()
		buf := make([]byte, terminalChunk)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				touch()
				wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
				werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { // browser → shell
		defer func() { done <- struct{}{} }()
		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			touch()
			switch typ {
			case websocket.MessageBinary:
				if _, err := sess.Write(data); err != nil {
					return
				}
			case websocket.MessageText:
				var c terminalControl
				if json.Unmarshal(data, &c) != nil || c.Type != "resize" ||
					c.Rows == 0 || c.Cols == 0 || c.Rows > maxResize || c.Cols > maxResize {
					continue
				}
				if err := sess.Resize(ctx, c.Rows, c.Cols); err != nil && ctx.Err() == nil {
					slog.Debug("terminal resize", "app", app.Name, "err", err)
				}
			}
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
	cancel()
	sess.Close()
	<-idle
	if !idled.Load() {
		conn.Close(websocket.StatusNormalClosure, "")
	}
}
