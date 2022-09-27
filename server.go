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
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rapidloop/rapidrows/qjs"

	"github.com/cespare/xxhash/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/robfig/cron/v3"
	"github.com/rs/cors"
	"github.com/rs/zerolog"
)

const (
	readTimeout  = time.Minute
	writeTimeout = 5 * time.Minute
	idleTimeout  = 2 * time.Minute
)

// APIServer is the backend server that will respond to HTTP requests are
// specified in an APIServerConfiguration. For some features, it relies on
// external dependencies which are injected using a RuntimeInterface object.
type APIServer struct {
	cfg         *APIServerConfig
	rti         *RuntimeInterface
	srv         *http.Server
	logger      zerolog.Logger
	ds          *datasources
	pinfo       sync.Map // parameter information
	nd          sync.Map // datasource name -> notification dispatcher
	c           *cron.Cron
	bgctx       context.Context
	bgctxcancel context.CancelFunc
}

// NewAPIServer creates a new APIServer object, given a server configuration
// object and an optional runtime interface. The configuration must be valid,
// otherwise an error is returned. The runtime interface, while optional, is
// required for the caching and logging.
func NewAPIServer(cfg *APIServerConfig, rti *RuntimeInterface) (*APIServer, error) {
	if cfg == nil {
		return nil, errors.New("invalid configuration: is nil")
	}
	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	a := &APIServer{
		cfg: cfg,
		rti: rti,
		ds:  new(datasources),
	}

	// setup logger
	if rti == nil || rti.Logger == nil {
		a.logger = zerolog.Nop()
	} else if rti != nil && rti.Logger != nil {
		a.logger = *rti.Logger
	}
	a.ds.logger = a.logger

	// setup cron
	a.c = newCron(a.logger)

	return a, nil
}

// Start the API server. Upon startup, connections to datasources will be
// established and an HTTP server will be started on the specified port.
func (a *APIServer) Start() (err error) {
	// create a cancellable context for running background tasks
	a.bgctx, a.bgctxcancel = context.WithCancel(context.Background())

	// prepare, cache
	a.prepareParams()

	// connect to datasources
	if err := a.ds.start(a.bgctx, a.cfg.Datasources); err != nil {
		a.logger.Error().Err(err).Msg("failed to connect to all datasources")
		return err
	}

	// setup & start notification dispatchers
	if err := a.startNotifDispatchers(); err != nil {
		return err // already logged
	}

	// setup jobs & start cron
	if err := a.setupJobs(); err != nil {
		return err // already logged
	}
	a.c.Start()

	// setup & start http server
	r := chi.NewRouter()
	a.setupRouter(r)
	var h http.Handler = r
	if a.cfg.Compression {
		h = middleware.Compress(5)(h)
	}
	l := a.cfg.Listen
	if !rxPort.MatchString(l) {
		l += ":8080"
	}
	lnr, err := net.Listen("tcp", l)
	if err != nil {
		return err
	}
	a.srv = &http.Server{
		Addr:         a.cfg.Listen,
		Handler:      h,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
	go a.srv.Serve(lnr)
	a.logger.Info().Str("listen", l).Msg("API server started successfully")

	return nil
}

// Stop the server. The server will wait for up to the specified timeout for
// database queries to be cancelled and connections closed.
func (a *APIServer) Stop(timeout time.Duration) error {
	if a.srv == nil {
		return nil
	}

	a.logger.Info().Float64("timeout", float64(timeout)/1e6).
		Msg("stop request received, shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// stop cron
	a.c.Stop()
	a.bgctxcancel()
	<-a.bgctx.Done()

	// stop notification dispatchers
	a.stopNotifDispatchers()

	// stop running handlers & http server
	if err := a.srv.Shutdown(ctx); err != nil {
		return err
	}
	a.srv = nil

	// stop datasources
	a.ds.stop()

	a.logger.Info().Msg("API server stopped")
	return nil
}

type loggerForCORS struct { // implements cors.Logger
	logger zerolog.Logger
}

func (l *loggerForCORS) Printf(f string, args ...interface{}) {
	l.logger.Debug().Msgf(f, args...)
}

func (a *APIServer) setupRouter(r *chi.Mux) {
	// setup cors
	if corsCfg := a.cfg.CORS; corsCfg != nil {
		options := cors.Options{
			AllowedOrigins:   corsCfg.AllowedOrigins,
			AllowedMethods:   corsCfg.AllowedMethods,
			AllowedHeaders:   corsCfg.AllowedHeaders,
			ExposedHeaders:   corsCfg.ExposedHeaders,
			AllowCredentials: corsCfg.AllowCredentials,
			Debug:            corsCfg.Debug,
		}
		if corsCfg.MaxAge != nil && *corsCfg.MaxAge > 0 {
			options.MaxAge = *corsCfg.MaxAge
		}
		c := cors.New(options)
		if corsCfg.Debug {
			c.Log = &loggerForCORS{logger: a.logger.With().Bool("cors", true).Logger()}
		}
		r.Use(c.Handler)
	}

	// setup each endpoint
	for i := range a.cfg.Endpoints {
		a.setupEndpoint(r, &a.cfg.Endpoints[i])
	}

	// setup each stream
	for i := range a.cfg.Streams {
		a.setupStream(r, &a.cfg.Streams[i])
	}
}

func (a *APIServer) setupEndpoint(r *chi.Mux, ep *Endpoint) {
	var handler http.HandlerFunc = func(resp http.ResponseWriter, req *http.Request) {
		a.serve(resp, req, ep)
	}

	if len(ep.Methods) == 0 {
		r.HandleFunc(a.cfg.CommonPrefix+ep.URI, handler)
	} else {
		for _, m := range ep.Methods {
			r.Method(m, a.cfg.CommonPrefix+ep.URI, handler)
		}
	}
}

func (a *APIServer) reportMetric(name string, value float64, labels ...string) {
	if a.rti != nil && a.rti.ReportMetric != nil {
		a.rti.ReportMetric(name, labels, value)
	}
}

// getRealIP returns the originating IP address for the HTTP request.
func getRealIP(r *http.Request) string {
	// 1. if "X-Forwarded-For" is set, use the first one from it
	if ff := r.Header.Get("X-Forwarded-For"); len(ff) > 0 {
		if p := strings.Index(ff, ","); p != -1 {
			ff = ff[:p]
		}
		return ff
	}

	// 2. if "X-Real-Ip" header is set, use that
	if rip := r.Header.Get("X-Real-Ip"); len(rip) > 0 {
		return rip
	}

	// 3. use remote addr of socket
	ip := r.RemoteAddr
	if p := strings.LastIndex(ip, ":"); p != -1 {
		ip = ip[:p]
	}
	return ip
}

func (a *APIServer) serve(resp http.ResponseWriter, req *http.Request,
	ep *Endpoint) {
	t0 := time.Now()

	// setup logger
	uri := a.cfg.CommonPrefix + ep.URI
	logger := a.logger.With().Str("endpoint", uri).Logger()

	// get params
	params, err := a.getParams(req, ep, logger)
	if err != nil {
		logger.Error().Err(err).Msg("failed to get valid parameter values from client")
		http.Error(resp, "invalid parameter values", http.StatusBadRequest)
		return
	}

	// debug logging: handler start, params etc
	if ep.Debug {
		e := logger.Debug()
		if len(params) > 0 {
			paramsb, _ := json.Marshal(params)
			e = e.Str("params", string(paramsb))
		}
		e.Str("ip", getRealIP(req)).Msg("handler start")
	}

	// actually serve
	switch ep.ImplType {
	case "static-text", "static-json":
		a.serveStatic(resp, req, ep, logger)
	case "query-json", "query-csv":
		a.serveQuery(resp, req, ep, params, logger)
	case "exec":
		a.serveExec(resp, req, ep, params, logger)
	case "javascript":
		a.runScriptHandler(resp, req, ep, params, logger)
	default: // should not happen with valid config
		http.Error(resp, "invalid impltype", http.StatusInternalServerError)
	}

	// metrics
	elapsed := time.Since(t0)
	a.reportMetric("epserve", float64(elapsed)/1e6, "endpoint="+uri)

	// debug logging: handler end, time taken
	if ep.Debug {
		logger.Debug().Float64("elapsed", float64(elapsed)/1e6).Msg("handler end")
	}
}

// serveQuery handles a query-json or query-csv type endpoint.
func (a *APIServer) serveQuery(resp http.ResponseWriter, req *http.Request,
	ep *Endpoint, params []any, logger zerolog.Logger) {

	// Do debug logs only if debugging is turned on for this endpoint. Caller
	// can also wrap in "if ep.Debug" to avoid compute.
	debug := func() *zerolog.Event {
		e := logger.Debug()
		if !ep.Debug {
			e = e.Discard()
		}
		return e
	}

	// helper function for writing header
	var contentType string
	var encoder func(*queryResult, io.Writer) error
	if ep.ImplType == "query-json" {
		contentType = "application/json"
		encoder = qr2json
	} else {
		contentType = "text/csv; charset=utf-8"
		encoder = qr2csv
	}

	// caching support: fetch from cache if configured
	var cacheTTLNanos uint64
	if ep.Cache != nil && *ep.Cache > 0 {
		cacheTTLNanos = uint64(*ep.Cache * float64(time.Second))
	}
	useCache := cacheTTLNanos > 0 && a.rti != nil && a.rti.CacheSet != nil && a.rti.CacheGet != nil
	var cacheKey uint64
	if useCache {
		cacheKey = makeCacheKey(a.cfg.CommonPrefix+ep.URI, params, logger)
		if cacheKey == 0 {
			// should not happen, error computing cache key
			logger.Error().Msg("internal error computing cache key, won't cache this one")
			useCache = false
			// continue to the actual query
		} else if val, ok := a.rti.CacheGet(cacheKey); ok && len(val) >= 8 {
			// got data from cache, check TTL
			elapsed := uint64(time.Now().UnixNano()) - binary.BigEndian.Uint64(val[0:8])
			if elapsed <= cacheTTLNanos {
				debug().Uint64("cachekey", cacheKey).Msg("cache hit, cache still valid, serving from cache")
				// cached object is valid, write header & body
				resp.Header().Set("Content-Type", contentType)
				resp.Header().Set("Content-Length", strconv.Itoa(len(val[8:])))
				if _, err := resp.Write(val[8:]); err != nil {
					logger.Error().Err(err).Msg("error writing response")
				}
				return // we're done serving the query from the cache
			} else {
				// cached results too old, delete from cache
				debug().Uint64("cachekey", cacheKey).Msg("cache hit but value is stale, deleting")
				a.rti.CacheSet(cacheKey, nil)
				// continue to the actual query
			}
		} else {
			// not found in cache, go ahead to the actual query
			debug().Uint64("cachekey", cacheKey).Msg("cache miss")
		}
	}

	// make context
	ctx := a.bgctx
	if ep.Timeout != nil && *ep.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*ep.Timeout*float64(time.Second)))
		defer cancel()
	}

	// perform query
	qr := queryResult{Rows: make([][]any, 0)}
	tq := time.Now()
	cb := func(q querier) error {
		rows, err := q.Query(ctx, ep.Script, params...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				return err
			}
			qr.Rows = append(qr.Rows, vals)
		}
		return rows.Err()
	}
	if err := a.ds.withTx(ep.Datasource, ep.TxOptions, cb); err != nil {
		logger.Error().Err(err).Msg("query failed")
		// send error as a JSON object with response code 500
		qr.Rows = nil
		qr.Error = err.Error()
		resp.Header().Set("Content-Type", "application/json")
		resp.WriteHeader(http.StatusInternalServerError)
		if err2 := qr2json(&qr, resp); err2 != nil {
			logger.Error().Err(err2).Msg("error writing response")
		}
		return
	}
	debug().Float64("elapsed", float64(time.Since(tq)/1e6)).
		Msg("query completed successfully")

	// write header
	resp.Header().Set("Content-Type", contentType)

	// write body directly (not caching) or write to resp+bytes.Buffer (if caching)
	var out io.Writer
	var cacheValueBuf *bytes.Buffer
	if useCache {
		cacheValueBuf = &bytes.Buffer{}
		binary.Write(cacheValueBuf, binary.BigEndian, uint64(time.Now().UnixNano()))
		out = io.MultiWriter(resp, cacheValueBuf)
	} else {
		out = resp
	}
	if err := encoder(&qr, out); err != nil {
		logger.Error().Err(err).Msg("error writing response")
	} else if useCache && cacheKey > 0 {
		// if caching, store the result in the cache
		debug().Uint64("cachekey", cacheKey).Int("valuelen", cacheValueBuf.Len()).
			Msg("storing result in cache")
		a.rti.CacheSet(cacheKey, cacheValueBuf.Bytes())
	}
}

var (
	startOfValue = []byte{2}
	endOfValue   = []byte{3}
)

// makeCacheKey returns a non-cryptographic 64-bit hash value over the URI
// and the specific set of arg values for a given endpoint call.
func makeCacheKey(uri string, args []any, logger zerolog.Logger) uint64 {
	// NOTE: the values in 'args' can only be nil, bool, int64, float64, string,
	// []bool, []int64, []float64 and []string. Out of this, nil, string and []string
	// have to be handled explicitly, rest can go to binary.Write directly.
	d := xxhash.New()

	// write uri
	d.Write(startOfValue)
	d.Write([]byte(uri))
	d.Write(endOfValue)

	// write args
	for _, a := range args {
		d.Write(startOfValue)
		if s, ok := a.(string); ok {
			d.WriteString(s)
		} else if sa, ok := a.([]string); ok {
			for _, s := range sa {
				d.Write(startOfValue)
				d.WriteString(s)
				d.Write(endOfValue)
			}
		} else if a != nil {
			if err := binary.Write(d, binary.BigEndian, a); err != nil {
				// should not happen for args that came of of getParams()
				logger.Error().Err(err).
					Msgf("makeCacheKey: binary.Write: value of type %T %v", a, a)
				return 0
			}
		}
		// no data between sOV and eOV for 'nil's
		d.Write(endOfValue)
	}

	// calculate hash
	return d.Sum64()
}

// qr2json encodes a queryResult into JSON and writes it to an io.Writer.
func qr2json(qr *queryResult, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(qr)
}

// qr2csv encodes a queryResult into CSV and writes it to an io.Writer.
func qr2csv(qr *queryResult, w io.Writer) error {
	nrows := len(qr.Rows)
	if nrows == 0 {
		return nil
	}
	ncols := len(qr.Rows[0])
	strrow := make([]string, ncols)

	enc := csv.NewWriter(w)
	for _, row := range qr.Rows {
		for i := range row {
			strrow[i] = fmt.Sprintf("%v", row[i])
		}
		if err := enc.Write(strrow); err != nil {
			return err
		}
	}
	enc.Flush()
	return enc.Error()
}

// serveStatic handles a static text or json endpoint.
func (a *APIServer) serveStatic(resp http.ResponseWriter, req *http.Request,
	ep *Endpoint, logger zerolog.Logger) {

	// discard body, ignore errors
	_, _ = io.CopyN(io.Discard, req.Body, 4096)

	// write header
	data := []byte(ep.Script)
	if ep.ImplType == "static-json" {
		resp.Header().Set("Content-Type", "application/json")
	} else {
		resp.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	resp.Header().Set("Content-Length", strconv.Itoa(len(data)))

	// write body
	if _, err := resp.Write([]byte(ep.Script)); err != nil {
		logger.Error().Err(err).Msg("error writing response")
	}
}

// serveExec handles a exec type endpoint.
func (a *APIServer) serveExec(resp http.ResponseWriter, req *http.Request,
	ep *Endpoint, params []any, logger zerolog.Logger) {

	// Do debug logs only if debugging is turned on for this endpoint. Caller
	// can also wrap in "if ep.Debug" to avoid compute.
	debug := func() *zerolog.Event {
		e := logger.Debug()
		if !ep.Debug {
			e = e.Discard()
		}
		return e
	}

	// make context
	ctx := a.bgctx
	if ep.Timeout != nil && *ep.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*ep.Timeout*float64(time.Second)))
		defer cancel()
	}

	// run query
	var er execResult
	tq := time.Now()
	cb := func(q querier) error {
		tag, err := q.Exec(ctx, ep.Script, params...)
		if err != nil {
			return err
		}
		er.RowsAffected = tag.RowsAffected()
		return nil
	}
	if err := a.ds.withTx(ep.Datasource, ep.TxOptions, cb); err != nil {
		logger.Error().Err(err).Msg("exec failed")
		er.Error = err.Error()
		resp.Header().Set("Content-Type", "application/json")
		resp.WriteHeader(http.StatusInternalServerError)
		if err2 := json.NewEncoder(resp).Encode(er); err2 != nil {
			logger.Error().Err(err2).Msg("error writing response")
		}
		return
	}
	debug().Float64("elapsed", float64(time.Since(tq)/1e6)).Msg("exec completed successfully")

	// write output
	resp.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(resp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(er); err != nil {
		logger.Error().Err(err).Msg("error writing response")
	}
}

//------------------------------------------------------------------------------
// runtime interface

// RuntimeInterface provides the necessary support functions for logging,
// caching etc. All the functions here may be called from different goroutines
// simultaneously, so they must be goroutine-safe. They must also be efficient;
// the performance of the APIServer can be impacted if these functions are slow.
type RuntimeInterface struct {
	// Logger specifies where to send the logs to. The debug logs enabled with
	// the 'debug' options at various places in the configuration will emit
	// zerolog debug events. The only other levels used are error, warning
	// and info. If this field is nil, no logs will be emitted.
	Logger *zerolog.Logger

	// ReportMetric will be called for reporting the value of metrics, like
	// time taken to serve an endpoint etc. This function should finish as
	// quick as possible (eg, push the values into a channel and return).
	ReportMetric func(name string, labels []string, value float64)

	// CacheSet will be called to store or delete a cache entry. If value is
	// nil, the entry can be deleted.
	CacheSet func(key uint64, value []byte)

	// CacheGet will be called to retreive a cache entry. The function should
	// return whether the value was present or not also.
	CacheGet func(key uint64) (value []byte, found bool)

	// InitJSCtx is called to perform further optional initialization of the
	// javascript context.
	InitJSCtx func(ctx *qjs.Context)
}
