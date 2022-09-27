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
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/rs/zerolog"
)

type datasources struct {
	logger   zerolog.Logger
	pools    sync.Map
	timeouts sync.Map
	bgctx    context.Context
}

func (d *datasources) start(bgctx context.Context, sources []Datasource) error {
	// store bgctx for use as parent of background contexts we create
	d.bgctx = bgctx

	// connect to each source
	for i := range sources {
		s := &sources[i]
		pool, err := dsconnect(bgctx, s)
		if err != nil {
			d.logger.Error().Str("datasource", s.Name).Err(err).Msg("failed to connect to datasource")
			d.stop()
			return err
		} else {
			d.logger.Info().Str("datasource", s.Name).Msg("successfully connected to datasource")
			d.pools.Store(s.Name, pool)
			if s.Timeout != nil && *s.Timeout > 0 {
				d.timeouts.Store(s.Name, time.Duration(*s.Timeout*float64(time.Second)))
			}
		}
	}
	return nil
}

func dsconnect(ctx context.Context, s *Datasource) (pool *pgxpool.Pool, err error) {
	// create config
	cfg, err := ds2cfg(s)
	if err != nil {
		return
	}

	// create context
	if s.Timeout != nil && *s.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*s.Timeout*float64(time.Second)))
		defer cancel()
	}

	// connect
	pool, err = pgxpool.ConnectConfig(ctx, cfg)
	return
}

func ds2cfg(s *Datasource) (*pgxpool.Config, error) {
	// regular params
	cfg, err := pgxpool.ParseConfig(ds2url(s))
	if err != nil {
		return nil, err
	}

	// simple protocol
	if s.PreferSimpleProtocol {
		cfg.ConnConfig.PreferSimpleProtocol = true
	}

	// pool params
	if p := s.Pool; p != nil {
		if p.MinConns != nil && *p.MinConns > 0 && *p.MinConns <= math.MaxInt32 {
			cfg.MinConns = int32(*p.MinConns)
		}
		if p.MaxConns != nil && *p.MaxConns > 0 && *p.MaxConns <= math.MaxInt32 {
			cfg.MaxConns = int32(*p.MaxConns)
		}
		if p.MaxIdleTime != nil && *p.MaxIdleTime > 0 {
			cfg.MaxConnIdleTime = time.Duration(*p.MaxIdleTime * float64(time.Second))
		}
		if p.MaxConnectedTime != nil && *p.MaxConnectedTime > 0 {
			cfg.MaxConnLifetime = time.Duration(*p.MaxConnectedTime * float64(time.Second))
		}
		if p.Lazy {
			cfg.LazyConnect = true
		}
	}

	// role
	if len(s.Role) > 0 {
		// note: the "SET ROLE" does not take a bind parameter, so $1 type
		// arguments cannot be used. However, at this point s.Role is
		// guaranteed not to contain any special characters.
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			if _, err := conn.Exec(ctx, "SET ROLE "+s.Role); err != nil {
				return fmt.Errorf("failed to set role %q: %w", s.Role, err)
			}
			return nil
		}
	}

	return cfg, nil
}

func ds2url(s *Datasource) string {
	params := make(url.Values)
	set := func(s, kw string) {
		if len(s) > 0 {
			params.Set(kw, s)
		}
	}
	set(s.Host, "host")         // pass as query param, not userinfo
	set(s.User, "user")         // pass as query param, not userinfo
	set(s.Password, "password") // pass as query param, not userinfo
	set(s.Database, "dbname")   // pass as query param, not userinfo
	set(s.Passfile, "passfile")
	set(s.SSLMode, "sslmode")
	set(s.SSLCert, "sslcert")
	set(s.SSLKey, "sslkey")
	set(s.SSLRootCert, "sslrootcert")
	for k, v := range s.Params {
		params.Set(k, v)
	}

	// set connection timeout from s.Timeout
	if s.Timeout != nil && *s.Timeout > 0 {
		params.Set("connect_timeout", strconv.Itoa(int(math.Round(*s.Timeout))))
	}
	// note: we're also using context deadline for this instead, only 1 is
	// required probably

	return "postgres://?" + params.Encode()
}

func (d *datasources) get(name string) (*pgxpool.Pool, error) {
	v, ok := d.pools.Load(name)
	if !ok || v == nil {
		return nil, fmt.Errorf("datasource %q not found", name) // should not happen
	}
	pool, _ := v.(*pgxpool.Pool)
	return pool, nil
}

func (d *datasources) withConn(name string, cb func(conn *pgxpool.Conn) error) error {
	// get pool
	pool, err := d.get(name)
	if err != nil {
		return err
	}

	// create context
	ctx := d.bgctx
	if t, ok := d.timeouts.Load(name); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.(time.Duration))
		defer cancel()
	}

	// acquire conn
	return pool.AcquireFunc(ctx, cb)
}

func (d *datasources) acquire(name string, timeout time.Duration) (*pgxpool.Conn, error) {
	// get pool
	pool, err := d.get(name)
	if err != nil {
		return nil, err
	}

	// create context
	ctx := d.bgctx
	if timeout > 0 {
		// try to use supplied timeout if valid
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	} else if t, ok := d.timeouts.Load(name); ok {
		// else use the timeout configured along with the datasource, if present
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.(time.Duration))
		defer cancel()
	}

	// acquire conn
	return pool.Acquire(ctx)
}

func (d *datasources) hijack(name string) (conn *pgx.Conn, err error) {
	// get pool
	pool, err := d.get(name)
	if err != nil {
		return nil, err
	}

	// acquire one
	poolConn, err := pool.Acquire(d.bgctx)
	if err != nil {
		return
	}

	// hijack it
	conn = poolConn.Hijack()
	return
}

type querier interface {
	Exec(ctx context.Context, sql string, arguments ...interface{}) (commandTag pgconn.CommandTag, err error)
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
}

func (d *datasources) withTx(name string, txopt *TxOptions, cb func(q querier) error) error {
	// if tx is nil, reduce this to withConn
	if txopt == nil {
		adapter1 := func(conn *pgxpool.Conn) error { return cb(conn) }
		return d.withConn(name, adapter1)
	}

	// get pool
	pool, err := d.get(name)
	if err != nil {
		return err
	}

	// create context
	ctx := context.Background()
	if t, ok := d.timeouts.Load(name); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.(time.Duration))
		defer cancel()
	}

	// acquire conn and call cb in a tx
	opt := pgx.TxOptions{
		AccessMode:     pgx.TxAccessMode(strings.ToLower(txopt.Access)),
		IsoLevel:       pgx.TxIsoLevel(strings.ToLower(txopt.ISOLevel)),
		DeferrableMode: pgx.TxDeferrableMode(pick(txopt.Deferrable, "deferrable", "not deferrable")),
	}
	adapter2 := func(tx pgx.Tx) error { return cb(tx) }
	return pool.BeginTxFunc(ctx, opt, adapter2)
}

func (d *datasources) stop() {
	d.pools.Range(func(k, v any) bool {
		name, _ := k.(string)
		pool, _ := v.(*pgxpool.Pool)
		pool.Close()
		d.logger.Info().Str("datasource", name).Msg("datasource connection pool closed")
		return true
	})
}
