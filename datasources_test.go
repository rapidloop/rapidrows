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
	"testing"
	"time"

	"github.com/rapidloop/rapidrows"
	"github.com/stretchr/testify/require"
)

const cfgTestDatasourcesBasic = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "query-json",
			"script": "select count(*) from pg_stat_activity where application_name='rrdstest'",
			"datasource": "ds1"
		},
		{
			"uri": "/username",
			"implType": "javascript",
			"script": "$sys.result = $sys.acquire('ds1').query('select current_user').rows[0][0]",
			"datasource": "ds1"
		},
		{
			"uri": "/setup",
			"implType": "exec",
			"script": "drop table if exists movies; create table movies (name text, year integer); insert into movies values ('The Shawshank Redemption', 1994), ('The Godfather', 1972), ('The Dark Knight', 2008), ('The Godfather Part II', 1974), ('12 Angry Men', 1957);",
			"datasource": "ds1"
		},
		{
			"uri": "/tx-rw-s",
			"implType": "exec",
			"tx": { "access": "read write", "level": "serializable" },
			"script": "update movies set name='x' where name='y'",
			"datasource": "ds1"
		},
		{
			"uri": "/tx-ro-s",
			"implType": "exec",
			"tx": { "access": "read only", "level": "serializable" },
			"script": "update movies set name='x' where name='y'",
			"datasource": "ds1"
		},
		{
			"uri": "/tx-ro-s-d",
			"implType": "exec",
			"tx": { "access": "read only", "level": "serializable", "deferrable": true },
			"script": "update movies set name='x' where name='y'",
			"datasource": "ds1"
		},
		{
			"uri": "/tx-rw-rr",
			"implType": "exec",
			"tx": { "level": "repeatable read" },
			"script": "update movies set name='x' where name='y'",
			"datasource": "ds1"
		}
	],
	"datasources": [
		{
			"name": "ds1",
			"timeout": 10,
			"pool": {
				"minConns": 5,
				"maxConns": 10,
				"maxIdleTime": 60,
				"maxConnectedTime": 120
			},
			"params": {
				"application_name": "rrdstest"
			}
		}
	]
}`

const expPool1 = `{
  "rows": [
    [
      5
    ]
  ]
}
`

const expPool2 = `{
  "rows": [
    [
      1
    ]
  ]
}
`

func TestDatasourcesAcquire(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestDatasourcesBasic)
	s := startServerFull(r, cfg)
	body, resp := doGet(r, "http://127.0.0.1:60000/")
	r.Equal(200, resp.StatusCode)
	r.Equal(expPool1, string(body))
	s.Stop(time.Second)

	cfg.Datasources[0].Pool.Lazy = true
	s = startServerFull(r, cfg)
	body, resp = doGet(r, "http://127.0.0.1:60000/")
	r.Equal(200, resp.StatusCode)
	r.Equal(expPool2, string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/username")
	r.Equal(200, resp.StatusCode)
	r.NotEqual("", string(body))
	roleName := string(body)

	s.Stop(time.Second)

	cfg.Datasources[0].Role = roleName
	s = startServerFull(r, cfg)
	body, resp = doGet(r, "http://127.0.0.1:60000/")
	r.Equal(200, resp.StatusCode)
	r.Equal(expPool2, string(body))
	s.Stop(time.Second)

	cfg.Datasources[0].Role = "hopefully$no$such$role"
	cfg.Datasources[0].Pool.Lazy = false
	s, err := rapidrows.NewAPIServer(cfg, nil)
	r.NotNil(s, "error was %v", err)
	r.Nil(err)
	err = s.Start()
	r.NotNil(err)
	r.EqualError(err, `failed to set role "hopefully$no$such$role": ERROR: role "hopefully$no$such$role" does not exist (SQLSTATE 22023)`)
}

const expTxRO = `{"rowsAffected":0,"error":"ERROR: cannot execute UPDATE in a read-only transaction (SQLSTATE 25006)"}
`

func TestDatasourcesTx(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestDatasourcesBasic)
	s := startServerFull(r, cfg)

	body, resp := doGet(r, "http://127.0.0.1:60000/tx-rw-s")
	r.Equal(expMoviesTx1, string(body))
	r.Equal(200, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/tx-ro-s")
	r.Equal(expTxRO, string(body))
	r.Equal(500, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/tx-ro-s-d")
	r.Equal(expTxRO, string(body))
	r.Equal(500, resp.StatusCode)

	body, resp = doGet(r, "http://127.0.0.1:60000/tx-rw-rr")
	r.Equal(expMoviesTx1, string(body))
	r.Equal(200, resp.StatusCode)

	s.Stop(time.Second)
}

/*

const cfgTestDatasourcesNoConn = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "query-json",
			"script": "select count(*) from pg_stat_activity where application_name='rrdstest'",
			"datasource": "ds1",
			"timeout": 1
		},
		{
			"uri": "/sleep",
			"implType": "query-json",
			"script": "select pg_sleep(60)",
			"datasource": "ds1"
		}
	],
	"datasources": [
		{
			"name": "ds1",
			"timeout": 5,
			"pool": {
				"maxConns": 1
			},
			"params": {
				"application_name": "rrdstest"
			}
		}
	]
}`

func TestDatasourcesNoConn(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestDatasourcesNoConn)
	s := startServerFull(r, cfg)

	go func() {
		resp, err := http.Get("http://127.0.0.1:60000/sleep")
		r.Nil(err)
		r.NotNil(resp)
		//r.Equal(200, resp.StatusCode)
		r.NotNil(resp.Body)
		io.Copy(os.Stdout, resp.Body)
	}()

	time.Sleep(time.Second)
	body, resp := doGet(r, "http://127.0.0.1:60000/")
	fmt.Printf("==%s==\n", string(body))
	r.Equal(500, resp.StatusCode)

	s.Stop(time.Second)
}
*/
