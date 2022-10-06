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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/rapidloop/rapidrows/qjs"
	"github.com/rs/zerolog"
)

const jsPropSys = "$sys"

//------------------------------------------------------------------------------

type scriptContext struct {
	conns  map[string]*pgxpool.Conn
	ctx    *qjs.Context
	a      *APIServer
	logger zerolog.Logger
	debug  bool
}

func newScriptContext(ctx *qjs.Context, a *APIServer, logger zerolog.Logger,
	debug bool) *scriptContext {
	return &scriptContext{
		ctx:    ctx,
		conns:  make(map[string]*pgxpool.Conn),
		a:      a,
		logger: logger,
		debug:  debug,
	}
}

func (sctx *scriptContext) acquire(ctx *qjs.Context, this qjs.Value, args []qjs.Value) qjs.Value {
	// check args
	if n := len(args); n != 1 && n != 2 {
		sctx.logger.Error().Msgf("$sys.acquire: got %d args, need 1 or 2", n)
		return ctx.ThrowError("$sys.acquire: needs 1 or 2 arguments")
	}

	// first arg: required, string, valid data source name
	if args[0].Tag() != qjs.TagString {
		sctx.logger.Error().Msg("$sys.acquire: first argument not a string")
		return ctx.ThrowError("$sys.acquire: first argument must be datasource name (string)")
	}
	dsname, _ := args[0].Any().(string)
	if len(dsname) == 0 {
		sctx.logger.Error().Msg("$sys.acquire: first argument is an empty string")
		return ctx.ThrowError("$sys.acquire: datasource not specified")
	}
	found := false
	for i := range sctx.a.cfg.Datasources {
		if sctx.a.cfg.Datasources[i].Name == dsname {
			found = true
			break
		}
	}
	if !found {
		sctx.logger.Error().Msgf("$sys.acquire: unknown datasource %q", dsname)
		return ctx.ThrowError(fmt.Sprintf("$sys.acquire: unknown datasource %q", dsname))
	}

	// second arg: optional, integer, timeout
	var timeout time.Duration
	if len(args) == 2 {
		tag := args[1].Tag()
		if tag == qjs.TagInt {
			v, _ := args[1].Any().(int64)
			timeout = time.Duration(v) * time.Second
		} else if tag == qjs.TagFloat64 {
			v, _ := args[1].Any().(float64)
			timeout = time.Duration(float64(time.Second) * v)
		} else {
			sctx.logger.Error().Msg("$sys.acquire: second argument not a number")
			return ctx.ThrowError("$sys.acquire: second argument must be timeout in seconds (number)")
		}
	}

	// try to acquire & add to pool
	conn, err := sctx.a.ds.acquire(dsname, timeout)
	if err != nil {
		sctx.logger.Error().Err(err).Str("datasource", dsname).
			Msg("$sys.acquire: failed to acquire connection")
		return ctx.ThrowError(fmt.Sprintf("$sys.acquire(%q): %v", dsname, err))
	}
	sctx.conns[dsname] = conn

	// log if debug
	if sctx.debug {
		sctx.logger.Debug().Str("datasource", dsname).Msg("acquired connection")
	}

	// capture conn
	query := func(ctx *qjs.Context, this qjs.Value, args []qjs.Value) qjs.Value {
		return sctx.query(conn, ctx, this, args)
	}
	exec := func(ctx *qjs.Context, this qjs.Value, args []qjs.Value) qjs.Value {
		return sctx.exec(conn, ctx, this, args)
	}

	// create a javascript object, set methods and return
	connObj := ctx.Object()
	setfnProp(sctx.ctx, connObj, "query", query)
	setfnProp(sctx.ctx, connObj, "exec", exec)
	return connObj
}

func (sctx *scriptContext) query(conn *pgxpool.Conn, ctx *qjs.Context, this qjs.Value, args []qjs.Value) qjs.Value {
	// parse args
	q, sqlArgs, err := parseArgs("query", args)
	if err != nil {
		sctx.logger.Error().Err(err).Msg("bad input")
		return ctx.ThrowError(err.Error())
	}

	// actually query
	t1 := time.Now()
	qr := doQuery(sctx.a.bgctx, conn, q, sqlArgs...)
	if sctx.debug {
		elapsed := float64(time.Since(t1)) / 1e6
		if len(qr.Error) == 0 {
			sctx.logger.Debug().Float64("elapsed", elapsed).Msg("query completed successfully")
		}
	}

	// if query failed, throw error
	if len(qr.Error) != 0 {
		sctx.logger.Error().Str("error", qr.Error).Msg("query failed")
		return ctx.ThrowError(qr.Error)
	}

	// convert queryResult object to qjs object
	ret, err := ctx.ObjectViaJSON(qr)
	if err != nil { // should not happen
		sctx.logger.Error().Err(err).Msg("json encoding failed")
		return ctx.ThrowError(err.Error())
	}
	return ret
}

func (sctx *scriptContext) exec(conn *pgxpool.Conn, ctx *qjs.Context, _ qjs.Value, args []qjs.Value) qjs.Value {
	// parse args
	q, sqlArgs, err := parseArgs("exec", args)
	if err != nil {
		sctx.logger.Error().Err(err).Msg("bad input")
		return ctx.ThrowError(err.Error())
	}

	// actually exec
	t1 := time.Now()
	er := doExec(sctx.a.bgctx, conn, q, sqlArgs...)
	if sctx.debug {
		elapsed := float64(time.Since(t1)) / 1e6
		if len(er.Error) == 0 {
			sctx.logger.Debug().Float64("elapsed", elapsed).Msg("exec query completed successfully")
		}
	}

	// if exec query failed, throw error
	if len(er.Error) != 0 {
		sctx.logger.Error().Str("error", er.Error).Msg("exec query failed")
		return ctx.ThrowError(er.Error)
	}

	// convert execResult object to qjs object
	ret, err := ctx.ObjectViaJSON(er)
	if err != nil { // should not happen
		sctx.logger.Error().Err(err).Msg("json encoding failed")
		return ctx.ThrowError(err.Error())
	}
	return ret
}

func (sctx *scriptContext) close() {
	for dsname, conn := range sctx.conns {
		conn.Release()
		if sctx.debug {
			sctx.logger.Debug().Str("datasource", dsname).Msg("released connection")
		}
	}
}

func setfnProp(ctx *qjs.Context, obj qjs.Value, name string, f qjs.Function) {
	fv := ctx.NewFunction(name, f)
	obj.SetProperty(name, fv)
}

// doQuery runs a sql query with args on a pgxpool connection and collects the
// resultset into a queryResult. If the query failed, the error will be present
// in queryResult.Error.
func doQuery(ctx context.Context, conn *pgxpool.Conn, query string, args ...any) (qr queryResult) {
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		qr.Error = err.Error()
		return
	}
	defer rows.Close()

	qr.Rows = make([][]any, 0)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			qr.Rows = nil
			qr.Error = err.Error()
			return
		}
		qr.Rows = append(qr.Rows, vals)
	}
	err = rows.Err()
	if err != nil {
		qr.Rows = nil
		qr.Error = err.Error()
	}

	return
}

// doExec runs a sql query with args on a pgxpool connection. It returns the
// rows affected or the error in an execResult.
func doExec(ctx context.Context, conn *pgxpool.Conn, query string, args ...any) (er execResult) {
	tag, err := conn.Exec(ctx, query, args...)
	if err != nil {
		er.Error = err.Error()
		return
	}
	er.RowsAffected = tag.RowsAffected()
	return
}

// parseArgs checks the arguments passed to the acquire.query() and
// acquire.exec() js functions.
func parseArgs(f string, args []qjs.Value) (q string, sqlArgs []any, err error) {
	// check arg count
	if len(args) < 1 {
		err = fmt.Errorf("$sys.%s: need at least 1 argument", f)
		return
	}

	// first arg: sql query
	if args[0].Tag() != qjs.TagString {
		err = fmt.Errorf("$sys.%s: first argument must be a SQL query (string)", f)
		return
	}
	q, _ = args[0].Any().(string)

	// rest of the args are passed to sql query
	sqlArgs = make([]any, len(args)-1)
	for i := 1; i < len(args); i++ {
		sqlArgs[i-1] = args[i].Any()
	}
	return
}

//------------------------------------------------------------------------------

func (a *APIServer) runScriptHandler(resp http.ResponseWriter, req *http.Request,
	ep *Endpoint, params []any, logger zerolog.Logger) {

	// convert params to a map
	paramsMap := make(map[string]any, len(ep.Params))
	for i := range ep.Params {
		paramsMap[ep.Params[i].Name] = params[i]
	}

	// actually run the script
	result, tag, err := a.runScript(ep.Script, paramsMap, logger, ep.Debug)

	// helper function to write string/object results
	writeResult := func(code int) bool {
		// is it a string?
		if tag == qjs.TagString {
			resp.Header().Set("Content-Type", "text/plain; charset=utf8")
			resp.WriteHeader(code)
			if _, err := resp.Write([]byte(result.(string))); err != nil {
				logger.Error().Err(err).Msg("error writing response")
			}
			return true
		}

		// else we'll also accept an object, as long as it is not an array
		if tag == qjs.TagObject {
			if _, ok := result.([]any); !ok {
				resp.Header().Set("Content-Type", "application/json")
				resp.WriteHeader(code)
				enc := json.NewEncoder(resp)
				enc.SetIndent("", "  ")
				if err := enc.Encode(result); err != nil {
					logger.Error().Err(err).Msg("error encoding result object")
				}
				return true
			}
		}

		return false
	}

	write500Body := func(body string) {
		resp.WriteHeader(500)
		if _, err := resp.Write([]byte(body)); err != nil {
			logger.Error().Err(err).Msg("error writing response")
		}
	}

	// did we get any result at all?
	noResult := tag == qjs.TagUndefined || tag == qjs.TagUninitialized || tag == qjs.TagNull

	// did it fail?
	if err != nil {
		if noResult {
			logger.Error().Err(err).Msg("script failed")
			write500Body(err.Error())
		} else {
			if writeResult(500) {
				logger.Error().Err(err).Msg("script failed with result")
			} else {
				logger.Error().Msg("script failed, also unsupported result type from script")
				write500Body("script error")
			}
		}
		return
	}

	// success, but no $sys.result
	if noResult {
		resp.WriteHeader(204)
		return
	}

	// success with $sys.result of supported type
	if writeResult(200) {
		return
	}

	// $sys.result is not usable
	http.Error(resp, "unsupported result type from script", http.StatusInternalServerError)
	logger.Error().Msg("unsupported result type from script")
}

func (a *APIServer) runScript(script string, paramsMap map[string]any,
	logger zerolog.Logger, debug bool) (result any, tag int, err error) {
	// setup javascript env
	rt := qjs.NewRuntime()
	ctx := rt.NewContext()
	sctx := newScriptContext(ctx, a, logger, debug)

	// create and set the $sys object
	sys := ctx.Object()
	// set params
	paramsObj, _ := ctx.ObjectViaJSON(paramsMap)
	sys.SetProperty("params", paramsObj)
	// set acquire
	setfnProp(ctx, sys, "acquire", sctx.acquire)
	// set into global
	global := ctx.Global()
	global.SetProperty(jsPropSys, sys.Dup())

	// call rti hook
	if a.rti != nil && a.rti.InitJSCtx != nil {
		a.rti.InitJSCtx(ctx)
	}

	// run the script
	obj, errObj := ctx.Eval(script)
	obj.Free() // obj is the last evaluated expression of script, discard

	// on success, return $sys.result object as "result"
	if errObjTag := errObj.Tag(); errObjTag == qjs.TagUndefined {
		resultObj := sys.GetProperty("result")
		tag = resultObj.Tag()
		result = resultObj.Any()
		resultObj.Free()
	} else {
		// else return the errObj as "result", also set "err"
		tag = errObjTag
		result = errObj.Any()
		errObj.Free()
		msg := fmt.Sprintf("%v", result)
		if len(msg) == 0 {
			msg = "script error"
		}
		err = errors.New(msg)
	}

	// cleanup
	sctx.close()
	sys.Free()
	global.Free()
	ctx.Free()
	rt.RunGC()
	rt.Free()
	return
}
