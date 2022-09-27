/*
 * Copyright 2022 RapidLoop, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rapidrows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/rs/zerolog"
	"nhooyr.io/websocket"
)

func (a *APIServer) startNotifDispatchers() error {
	// make a list of all channels that each datasource needs
	ds2pgchans := make(map[string][]string)
	add := func(ds, pgchan string) {
		if pgchans, ok := ds2pgchans[ds]; ok {
			for _, c := range pgchans {
				if c == pgchan {
					return // already present
				}
			}
			ds2pgchans[ds] = append(pgchans, pgchan)
		} else {
			ds2pgchans[ds] = []string{pgchan}
		}
	}
	for _, s := range a.cfg.Streams {
		add(s.Datasource, s.Channel)
	}

	// open a long-lived connection to each datasource, and start a notifDispatcher
	// on that
	connsToClose := make([]*pgx.Conn, 0, len(ds2pgchans))
	var err error
	for ds, pgchans := range ds2pgchans {
		nd := newNotifDispatcher(pgchans, a.logger)
		var conn *pgx.Conn
		conn, err = a.ds.hijack(ds)
		if err != nil {
			a.logger.Error().Str("datasource", ds).Err(err).
				Msg("failed to open connection")
			break
		}
		if err = nd.start(conn); err != nil { // note: err =, not err :=
			a.logger.Error().Str("datasource", ds).Err(err).
				Msg("failed to start notification dispatcher")
			break
		}
		a.nd.Store(ds, nd)
		connsToClose = append(connsToClose, conn)
		a.logger.Info().Str("datasource", ds).Strs("channels", pgchans).
			Msg("started notification dispatcher")
	}

	// on errors, close all opened connections
	if err != nil {
		for _, c := range connsToClose {
			c.Close(context.Background())
		}
		return err
	}

	return nil
}

func (a *APIServer) stopNotifDispatchers() {
	a.nd.Range(func(k, v any) bool {
		v.(*notifDispatcher).stop()
		a.logger.Info().Str("datasource", k.(string)).Msg("stopped notification dispatcher")
		return true
	})
}

func (a *APIServer) setupStream(r *chi.Mux, s *Stream) {
	var handler http.HandlerFunc = func(resp http.ResponseWriter, req *http.Request) {
		a.serveStream(resp, req, s)
	}

	r.HandleFunc(a.cfg.CommonPrefix+s.URI, handler)
}

func (a *APIServer) serveStream(resp http.ResponseWriter, req *http.Request, s *Stream) {
	// setup logger, debug logging
	logger := a.logger.With().Str("endpoint", a.cfg.CommonPrefix+s.URI).Logger()
	if s.Debug {
		logger.Debug().Str("channel", s.Channel).Str("datasource", s.Datasource).
			Str("type", s.Type).Msg("stream handler start")
	}

	// discard body, ignore errors
	_, _ = io.CopyN(io.Discard, req.Body, 4096)

	// get notifdispatcher for the stream's datasource
	var nd *notifDispatcher
	if v, ok := a.nd.Load(s.Datasource); ok && v != nil {
		nd = v.(*notifDispatcher)
	}
	if nd == nil {
		// should not happen
		logger.Error().Str("datasource", s.Datasource).
			Msg("internal error: notification dispatcher not found")
		http.Error(resp, "internal error", http.StatusInternalServerError)
		return
	}

	// do the main loop
	nw := newNotifWriter()
	nd.register(s.Channel, nw)
	var err error
	if s.Type == "websocket" {
		err = nw.loopWS(a.bgctx, resp, req, nil, false, a.logger)
	} else {
		err = nw.loopSSE(a.bgctx, resp, req, a.logger)
	}
	if !errors.Is(err, context.Canceled) {
		nd.unregister(s.Channel, nw)
	}
	// if bgctx was cancelled, that means the server is shutting down and
	// notifDispatcher might have gone away already

	// don't consider 'broken pipe' and 'i/o timeout' as errors to be logged
	if err != nil {
		if s := err.Error(); strings.Contains(s, "broken pipe") ||
			strings.Contains(s, "i/o timeout") {
			err = nil
		}
	}

	if err != nil {
		logger.Error().Err(err).Msg("stream closed on error")
	} else if s.Debug {
		logger.Debug().Str("channel", s.Channel).Str("datasource", s.Datasource).
			Str("type", s.Type).Msg("stream handler end")
	}
}

//------------------------------------------------------------------------------

// notifWriter writes out the payloads of pgconn.Notification objects into
// a *websocket.Conn. It does not have a dedicated goroutine, it's event loop
// is meant to be hosted by the http handler goroutine.
type notifWriter struct {
	q       chan string
	qClosed bool
	qMtx    sync.Mutex
}

// notifWriterBacklog is the max number of notifications that are allowed to
// be pending to write into the websocket. If a new notification is available
// and we still have these many waiting to be written, the websocket is closed.
const notifWriterBacklog = 16

func newNotifWriter() *notifWriter {
	return &notifWriter{
		q: make(chan string, notifWriterBacklog),
	}
}

// accept takes in a new notification. This must NOT block. It is called by
// the notifDispatcher. There is a race between client disconnects for various
// reasons and a new notification arriving, so handle the case that when we
// attempt to write to the channel or close it, it is already closed by the
// other goroutine. Alternative is to route a special message via the
// notifDispatcher and close the channel only here; but then there is also
// the case that the notifDispatcher might have gone at server exit, and only
// we are alive.
func (n *notifWriter) accept(payload string) {
	defer func() {
		if r := recover(); r != nil {
			if err, _ := r.(error); err != nil {
				if err.Error() == "send on closed channel" {
					n.closeQ()
				}
			}
		}
	}()

	select {
	case n.q <- payload:
	default:
		// our queue is full, we can't make the caller wait, so we abort
		n.closeQ()
	}
}

func (n *notifWriter) closeQ() {
	n.qMtx.Lock()
	if !n.qClosed {
		close(n.q)
		n.qClosed = true
	}
	n.qMtx.Unlock()
}

var (
	notifWriteTimeout = 10 * time.Second
	errTooSlow        = errors.New("aborting connection because it is too slow")
)

// loopWS upgrades the given connection to a websocket and writes out the pg
// notifications into it. This is meant to be called directly from the http
// handler goroutine. It will block until client disconnects or if there are
// other errors. notifWriter must not be reused after this exits.
func (n *notifWriter) loopWS(ctx context.Context, resp http.ResponseWriter,
	req *http.Request, origins []string, compression bool,
	logger zerolog.Logger) error {

	// close q if required when we exit
	qclosed := false
	defer func() {
		if !qclosed {
			n.closeQ()
		}
	}()

	// upgrade connection
	ws, err := websocket.Accept(resp, req, &websocket.AcceptOptions{
		InsecureSkipVerify: len(origins) == 0,
		OriginPatterns:     origins,
		CompressionMode:    pick(compression, websocket.CompressionContextTakeover, websocket.CompressionDisabled),
	})
	if err != nil {
		return err
	}
	defer ws.Close(websocket.StatusInternalError, "") // no-op if already closed

	// start a reader that will respond to pings, but cancels context if any
	// other messages are received (we don't expect any)
	ctx = ws.CloseRead(ctx)

	for {
		select {

		case payload, ok := <-n.q:
			if !ok {
				ws.Close(websocket.StatusPolicyViolation, "connection too slow")
				qclosed = true
				return errTooSlow
			}
			ctx2, cancel := context.WithTimeout(ctx, notifWriteTimeout)
			err := ws.Write(ctx2, websocket.MessageText, []byte(payload))
			cancel()
			if err != nil {
				if cs := websocket.CloseStatus(err); cs == websocket.StatusNormalClosure || cs == websocket.StatusGoingAway {
					err = nil
				}
				return err
			}

		case <-ctx.Done():
			ws.Close(websocket.StatusGoingAway, "server shutdown")
			return ctx.Err()
		}
	}
}

var (
	notifSSEKeepAliveInterval = time.Minute
	notifSSEKeepAliveComment  = []byte{':', '\n', '\n'}
)

// loopSSE is like loopWS, but for server-sent-events.
func (n *notifWriter) loopSSE(ctx context.Context, resp http.ResponseWriter,
	req *http.Request, logger zerolog.Logger) error {
	// send also a comment every minute to keep the connection alive
	ticker := time.NewTicker(notifSSEKeepAliveInterval)

	// cleanup on exit
	qclosed := false
	defer func() {
		if !qclosed {
			n.closeQ()
		}
		ticker.Stop()
	}()

	// try to flush data out after each event
	flusher, _ := resp.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	// keep-alive helper
	keepalive := func() error {
		if _, err := resp.Write(notifSSEKeepAliveComment); err != nil {
			return err
		}
		flush()
		return nil
	}

	// write out the sse header
	h := resp.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")

	// write out an initial comment to start the body
	if err := keepalive(); err != nil {
		return err
	}

	for {
		select {

		case <-ticker.C:
			if err := keepalive(); err != nil {
				return err
			}

		case payload, ok := <-n.q:
			if !ok {
				qclosed = true
				return errTooSlow
			}
			for _, line := range strings.Split(payload, "\n") {
				if _, err := fmt.Fprintf(resp, "data: %s\n", line); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(resp); err != nil {
				return err
			}
			flush()

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

//------------------------------------------------------------------------------

// notifDispatcher listens for notifications from a *pgconn
type notifDispatcher struct {
	in       chan pgconn.Notification
	cmd      chan notifDisptacherCmd
	pgchans  []string
	logger   zerolog.Logger
	wg       sync.WaitGroup
	stopping atomic.Bool
	conn     *pgx.Conn
}

func newNotifDispatcher(pgchans []string, logger zerolog.Logger) *notifDispatcher {
	return &notifDispatcher{
		in:      make(chan pgconn.Notification, 64),
		cmd:     make(chan notifDisptacherCmd, 1),
		pgchans: append([]string{}, pgchans...),
		logger:  logger,
	}
}

// start starts the notifDispatcher. On success, assumes control of conn; else
// caller has to cleanup conn.
func (nd *notifDispatcher) start(conn *pgx.Conn) error {
	// listen to all channels of interest
	for _, pgchan := range nd.pgchans {
		if _, err := conn.Exec(context.Background(), "LISTEN "+pgchan); err != nil {
			return fmt.Errorf("failed to LISTEN to %q: %v", pgchan, err)
		}
	}

	// ok, conn is ready, store it as a way to stop fetcher
	nd.conn = conn

	// start the dispatcher
	nd.wg.Add(1)
	go nd.dispatcher()

	// start the fetcher
	go nd.fetcher(conn)

	return nil
}

func (nd *notifDispatcher) stop() {
	// stop fetcher
	nd.stopping.Store(true)
	if err := nd.conn.Close(context.Background()); err != nil {
		nd.logger.Warn().Err(err).Msg("notification dispatcher: failed to close datasource connection")
	}

	// stop dispatcher
	nd.cmd <- notifDisptacherCmd{act: actStop}
	nd.wg.Wait()

	// cleanup
	close(nd.cmd)
	close(nd.in)
}

// fetcher keeps listening to notifications from conn, and pushes them into
// nd.in. To stop the fetcher, close the connection from another goroutine.
func (nd *notifDispatcher) fetcher(conn *pgx.Conn) {
	for {
		// wait forever for a notification
		n, err := conn.WaitForNotification(context.Background())
		if err != nil {
			if !nd.stopping.Load() { // ignore error if we're stopping
				nd.logger.Error().Err(err).Msg("failed to wait for notification from postgres")
			}
			return
		}

		// hand it off to the notifDispatcher's goroutine
		nd.in <- *n
	}
}

const (
	_ = iota
	actRegister
	actUnregister
	actStop
)

type notifDisptacherCmd struct {
	act     int
	channel string
	writer  *notifWriter
}

func (nd *notifDispatcher) register(pgchan string, writer *notifWriter) {
	nd.cmd <- notifDisptacherCmd{act: actRegister, channel: pgchan, writer: writer}
}

func (nd *notifDispatcher) unregister(pgchan string, writer *notifWriter) {
	nd.cmd <- notifDisptacherCmd{act: actUnregister, channel: pgchan, writer: writer}
}

func (nd *notifDispatcher) dispatcher() {
	// a map of pgchan -> all *notifWriter interested in that pgchan
	c2ws := make(map[string][]*notifWriter)
	unregister := func(c string, w2 *notifWriter) {
		if ws, ok := c2ws[c]; ok {
			for i, w := range ws {
				if w == w2 {
					ws[i] = nil
					copy(ws[i:], ws[i+1:])
					c2ws[c] = ws[:len(ws)-1]
					return
				}
			}
		}
	}

	for {
		select {
		case c := <-nd.cmd:
			switch c.act {
			case actRegister:
				c2ws[c.channel] = append(c2ws[c.channel], c.writer)
			case actUnregister:
				unregister(c.channel, c.writer)
			case actStop:
				nd.wg.Done()
				return
			}
		case notif := <-nd.in:
			for _, w := range c2ws[notif.Channel] {
				w.accept(notif.Payload)
			}
		}
	}
}

//------------------------------------------------------------------------------

func pick[T any](cond bool, ifyes, ifno T) T {
	if cond {
		return ifyes
	}
	return ifno
}
