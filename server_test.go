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

package rapidrows_test

import (
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/rapidloop/rapidrows"
	"github.com/rapidloop/rapidrows/qjs"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestServerInvalidCfg(t *testing.T) {
	r := require.New(t)

	s, err := rapidrows.NewAPIServer(nil, nil)
	r.Nil(s)
	r.NotNil(err)

	cfg := rapidrows.APIServerConfig{}
	s, err = rapidrows.NewAPIServer(&cfg, nil)
	r.Nil(s)
	r.NotNil(err)
}

const cfgTestServerBasic = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"compression": true,
	"cors": { "debug": true, "maxAge": 3600 },
	"endpoints": [
		{
			"uri": "/movies",
			"methods": [ "GET" ],
			"implType": "query-json",
			"script": "select * from movies order by year desc",
			"datasource": "default",
			"debug": true,
			"timeout": 60,
			"cache": 1
		},
		{
			"uri": "/movies-in-year/{year}",
			"implType": "query-json",
			"script": "select * from movies where year = $1 order by year desc",
			"datasource": "default",
			"cache": 1,
			"debug": true,
			"params": [
				{
					"name": "year",
					"in": "path",
					"type": "integer",
					"minimum": 1950,
					"maximum": 2030
				}
			]
		},
		{
			"uri": "/movies-in-year-no-cache/{year}",
			"implType": "query-json",
			"script": "select * from movies where year = $1 order by year desc",
			"datasource": "default",
			"params": [
				{
					"name": "year",
					"in": "path",
					"type": "integer",
					"minimum": 1950,
					"maximum": 2030
				}
			]
		},
		{
			"uri": "/movies-csv",
			"implType": "query-csv",
			"script": "select * from movies order by year desc",
			"datasource": "default",
			"cache": 3600
		},
		{
			"uri": "/setup",
			"implType": "exec",
			"script": "drop table if exists movies; create table movies (name text, year integer); insert into movies values ('The Shawshank Redemption', 1994), ('The Godfather', 1972), ('The Dark Knight', 2008), ('The Godfather Part II', 1974), ('12 Angry Men', 1957);",
			"datasource": "default",
			"debug": true,
			"timeout": 5
		},
		{
			"uri": "/movies-js",
			"implType": "javascript",
			"script": "$sys.result=$sys.acquire('default').query('select * from movies order by year desc')"
		},
		{
			"uri": "/movies-tx1",
			"implType": "exec",
			"tx": { "access": "read write", "level": "serializable" },
			"script": "update movies set name='x' where name='y'",
			"datasource": "default"
		},
		{
			"uri": "/info-json",
			"implType": "static-json",
			"script": "{\"apiVersion\":  1}"
		},
		{
			"uri": "/exec-error",
			"implType": "exec",
			"script": "syntax error",
			"datasource": "default"
		},
		{
			"uri": "/query-error",
			"implType": "query-json",
			"script": "syntax error",
			"datasource": "default"
		},
		{
			"uri": "/script-error-1",
			"implType": "javascript",
			"script": "*** syntax error"
		},
		{
			"uri": "/script-error-2",
			"implType": "javascript",
			"script": "$sys.acquire('no.such')"
		},
		{
			"uri": "/script-error-3",
			"implType": "javascript",
			"script": "$sys.acquire('default').query('syntax error')"
		},
		{
			"uri": "/script-error-4",
			"implType": "javascript",
			"script": "throw 'foo'"
		},
		{
			"uri": "/test-cache",
			"implType": "query-json",
			"script": "select $1 || array_to_string($2::text[], '')",
			"datasource": "default",
			"cache": 60,
			"params": [
				{
					"name": "param1",
					"in": "body",
					"type": "string"
				},
				{
					"name": "param2",
					"in": "body",
					"type": "array",
					"elemType": "string"
				}
			]
		},
		{
			"uri": "/test-csv-no-rows",
			"implType": "query-csv",
			"script": "select * from movies where year=1900",
			"datasource": "default"
		}
	],
	"streams": [
		{
			"uri": "/movies-changes",
			"type": "sse",
			"channel": "movieschanges",
			"datasource": "default",
			"debug": true
		},
		{
			"uri": "/movies-changes2",
			"type": "sse",
			"channel": "movieschanges",
			"datasource": "default"
		},
		{
			"uri": "/movies-changes3",
			"type": "sse",
			"channel": "movieschanges3",
			"datasource": "default"
		}
	],
	"datasources": [
		{
			"name": "default",
			"timeout": 5
		}
	]
}`

const expMoviesJson = `{
  "rows": [
    [
      "The Dark Knight",
      2008
    ],
    [
      "The Shawshank Redemption",
      1994
    ],
    [
      "The Godfather Part II",
      1974
    ],
    [
      "The Godfather",
      1972
    ],
    [
      "12 Angry Men",
      1957
    ]
  ]
}
`

const expMoviesCsv = `The Dark Knight,2008
The Shawshank Redemption,1994
The Godfather Part II,1974
The Godfather,1972
12 Angry Men,1957
`

const expMoviesTx1 = `{
  "rowsAffected": 0
}
`

const expMoviesInYear = `{
  "rows": [
    [
      "The Godfather",
      1972
    ]
  ]
}
`

const expMoviesTestCache = `{
  "rows": [
    [
      "foobarbaz"
    ]
  ]
}
`

func checkGetOK(r *require.Assertions, u string) {
	_, resp := doGet(r, u)
	r.Equal(200, resp.StatusCode)
}

func startServerFull(r *require.Assertions, cfg *rapidrows.APIServerConfig, dest ...io.Writer) *rapidrows.APIServer {
	var cache sync.Map
	cacheSet := func(key uint64, value []byte) {
		if len(value) == 0 {
			cache.Delete(key)
		} else {
			cache.Store(key, value)
		}
	}
	cacheGet := func(key uint64) (value []byte, found bool) {
		if v, ok := cache.Load(key); ok && v != nil {
			return v.([]byte), true
		}
		return nil, false
	}
	var logger zerolog.Logger
	if len(dest) > 0 {
		logger = zerolog.New(dest[0])
	} else {
		logger = zerolog.Nop()
	}
	rti := &rapidrows.RuntimeInterface{
		Logger:       &logger,
		CacheSet:     cacheSet,
		CacheGet:     cacheGet,
		ReportMetric: func(name string, labels []string, value float64) {},
		InitJSCtx:    func(ctx *qjs.Context) {},
	}
	s, err := rapidrows.NewAPIServer(cfg, rti)
	r.NotNil(s, "error was %v", err)
	r.Nil(err)
	r.Nil(s.Start())
	return s
}

func TestServerBasic(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestServerBasic)
	s := startServerFull(r, cfg)

	checkGetOK(r, "http://127.0.0.1:60000/setup")

	body, resp := doGet(r, "http://127.0.0.1:60000/movies")
	r.Equal(expMoviesJson, string(body))
	r.Equal(200, resp.StatusCode)

	// repeat both again, for testing cache
	body, resp = doGet(r, "http://127.0.0.1:60000/movies")
	r.Equal(expMoviesJson, string(body))
	r.Equal(200, resp.StatusCode)

	// wait for cache expiry
	time.Sleep(time.Second)
	body, resp = doGet(r, "http://127.0.0.1:60000/movies")
	r.Equal(expMoviesJson, string(body))
	r.Equal(200, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/movies-csv")
	r.Equal(expMoviesCsv, string(body))
	r.Equal(200, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/movies-csv")
	r.Equal(expMoviesCsv, string(body))
	r.Equal(200, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/movies-js")
	r.Equal(expMoviesJson, string(body))
	r.Equal(200, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/movies-tx1")
	r.Equal(expMoviesTx1, string(body))
	r.Equal(200, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/movies-in-year/1972")
	r.Equal(expMoviesInYear, string(body))
	r.Equal(200, resp.StatusCode)

	// repeat for cache
	body, resp = doGet(r, "http://127.0.0.1:60000/movies-in-year/1972")
	r.Equal(expMoviesInYear, string(body))
	r.Equal(200, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/movies-in-year-no-cache/1972")
	r.Equal(expMoviesInYear, string(body))
	r.Equal(200, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/info-json")
	r.Equal(`{"apiVersion":  1}`, string(body))
	r.Equal(200, resp.StatusCode)
	r.Equal("application/json", resp.Header.Get("Content-Type"))

	// query-json with a string param and cache
	// query-json with a []string param and cache
	v := url.Values{}
	v.Add("param1", "foo")
	v.Add("param2", "bar")
	v.Add("param2", "baz")
	body, resp = doPostForm(r, "http://127.0.0.1:60000/test-cache", v)
	r.Equal(expMoviesTestCache, string(body))
	r.Equal(200, resp.StatusCode)
	body, resp = doPostForm(r, "http://127.0.0.1:60000/test-cache", v)
	r.Equal(expMoviesTestCache, string(body))
	r.Equal(200, resp.StatusCode)

	// query-csv that returns no rows
	body, resp = doGet(r, "http://127.0.0.1:60000/test-csv-no-rows")
	r.Equal("", string(body))
	r.Equal(200, resp.StatusCode)

	s.Stop(time.Second)
}

const expExecError = `{"rowsAffected":0,"error":"ERROR: syntax error at or near \"syntax\" (SQLSTATE 42601)"}
`

const expQueryError = `{
  "rows": null,
  "error": "ERROR: syntax error at or near \"syntax\" (SQLSTATE 42601)"
}
`

func TestServerErrors(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestServerBasic)
	s := startServerFull(r, cfg)

	checkGetOK(r, "http://127.0.0.1:60000/setup")

	body, resp := doGet(r, "http://127.0.0.1:60000/exec-error")
	r.Equal(expExecError, string(body))
	r.Equal(500, resp.StatusCode)
	r.Equal("application/json", resp.Header.Get("Content-Type"))

	body, resp = doGet(r, "http://127.0.0.1:60000/query-error")
	r.Equal(expQueryError, string(body))
	r.Equal(500, resp.StatusCode)
	r.Equal("application/json", resp.Header.Get("Content-Type"))

	_, resp = doGet(r, "http://127.0.0.1:60000/script-error-1")
	r.Equal(500, resp.StatusCode)

	_, resp = doGet(r, "http://127.0.0.1:60000/script-error-2")
	r.Equal(500, resp.StatusCode)

	_, resp = doGet(r, "http://127.0.0.1:60000/script-error-3")
	r.Equal(500, resp.StatusCode)

	_, resp = doGet(r, "http://127.0.0.1:60000/script-error-4")
	r.Equal(500, resp.StatusCode)

	s.Stop(time.Second)
}

const cfgTestServerBadDS = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"datasources": [ { "name": "foo", "host": "192.0.0.1", "timeout": 2}]
}`

func TestServerStartupErrors(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestServerBadDS)
	logger := zerolog.Nop()
	rti := &rapidrows.RuntimeInterface{
		Logger: &logger,
	}
	s, err := rapidrows.NewAPIServer(cfg, rti)
	r.NotNil(s, "error was %v", err)
	r.Nil(err)
	r.NotNil(s.Start())
}

const cfgTestServerNoPort = `{
	"version": "1",
	"listen": "127.0.0.1"
}`

func TestServerStartupMisc(t *testing.T) {
	r := require.New(t)

	// (try to) start a listener on 8080, the start APIServer on default
	// port (which should be 8080)
	lnr, _ := net.Listen("tcp", "127.0.0.1:8080")
	cfg := loadCfg(r, cfgTestServerNoPort)
	s, err := rapidrows.NewAPIServer(cfg, nil)
	r.NotNil(s)
	r.Nil(err)
	err = s.Start()
	r.EqualError(err, "listen tcp 127.0.0.1:8080: bind: address already in use")
	if lnr != nil {
		lnr.Close()
	}

	// stop a server that is not started successfully
	r.Nil(s.Stop(time.Second))
}
