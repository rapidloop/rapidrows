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

	"github.com/stretchr/testify/require"
)

const cfgTestScriptBasic = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/acquire-1",
			"implType": "javascript",
			"script": "$sys.acquire(1,2,3)",
			"debug": true
		},
		{
			"uri": "/acquire-2",
			"implType": "javascript",
			"script": "$sys.acquire(1)",
			"debug": true
		},
		{
			"uri": "/acquire-3",
			"implType": "javascript",
			"script": "$sys.acquire('')",
			"debug": true
		},
		{
			"uri": "/acquire-4",
			"implType": "javascript",
			"script": "$sys.acquire('default', 10)",
			"debug": true
		},
		{
			"uri": "/acquire-5",
			"implType": "javascript",
			"script": "$sys.acquire('default', 10.5)",
			"debug": true
		},
		{
			"uri": "/acquire-6",
			"implType": "javascript",
			"script": "$sys.acquire('default', 'bad')",
			"debug": true
		},
		{
			"uri": "/acquire-7",
			"implType": "javascript",
			"script": "$sys.acquire('nosuchdatasource', 10)",
			"debug": true
		},

		{
			"uri": "/setup",
			"implType": "exec",
			"script": "drop table if exists movies; create table movies (name text, year integer); insert into movies values ('The Shawshank Redemption', 1994), ('The Godfather', 1972), ('The Dark Knight', 2008), ('The Godfather Part II', 1974), ('12 Angry Men', 1957);",
			"datasource": "default"
		},
		{
			"uri": "/query-1",
			"implType": "javascript",
			"script": "$sys.acquire('default').query()",
			"debug": true
		},
		{
			"uri": "/query-2",
			"implType": "javascript",
			"script": "$sys.acquire('default').query(100)",
			"debug": true
		},
		{
			"uri": "/query-3",
			"implType": "javascript",
			"script": "$sys.acquire('default').query('select * from movies where year=$1', 42)",
			"debug": true
		},

		{
			"uri": "/exec-1/{year}",
			"implType": "javascript",
			"script": "$sys.acquire('default').exec('update movies set year=year where year=$1', $sys.params.year)",
			"debug": true,
			"params": [
				{
					"name": "year",
					"type": "integer",
					"in": "path",
					"minimum": 1900, "maximum": 2050,
					"required": true
				}
			]
		},
		{
			"uri": "/exec-2",
			"implType": "javascript",
			"script": "$sys.acquire('default').exec('** syntax error')",
			"debug": true
		},
		{
			"uri": "/exec-3",
			"implType": "javascript",
			"script": "$sys.acquire('default').exec(42)",
			"debug": true
		},

		{
			"uri": "/result-1",
			"implType": "javascript",
			"script": "$sys.result = 'foo'",
			"debug": true
		},
		{
			"uri": "/result-2",
			"implType": "javascript",
			"script": "$sys.result = ['foo']",
			"debug": true
		}
	],
	"datasources": [ { "name": "default" } ]
}`

func TestScriptAcquire(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestScriptBasic)
	s := startServerFull(r, cfg)

	body, resp := doGet(r, "http://127.0.0.1:60000/acquire-1")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: $sys.acquire: needs 1 or 2 arguments\"\n}\n", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/acquire-2")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: $sys.acquire: first argument must be datasource name (string)\"\n}\n", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/acquire-3")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: $sys.acquire: datasource not specified\"\n}\n", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/acquire-4")
	r.NotNil(resp)
	r.Equal(204, resp.StatusCode)
	r.Equal(0, len(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/acquire-5")
	r.NotNil(resp)
	r.Equal(204, resp.StatusCode)
	r.Equal(0, len(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/acquire-6")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: $sys.acquire: second argument must be timeout in seconds (number)\"\n}\n", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/acquire-7")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: $sys.acquire: unknown datasource \\\"nosuchdatasource\\\"\"\n}\n", string(body))

	checkGetOK(r, "http://127.0.0.1:60000/setup")

	body, resp = doGet(r, "http://127.0.0.1:60000/query-1")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: $sys.query: need at least 1 argument\"\n}\n", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/query-2")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: $sys.query: first argument must be a SQL query (string)\"\n}\n", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/query-3")
	r.NotNil(resp)
	r.Equal(204, resp.StatusCode)
	r.Equal(0, len(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/exec-1/1972")
	r.NotNil(resp)
	r.Equal(204, resp.StatusCode)
	r.Equal(0, len(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/exec-2")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: ERROR: syntax error at or near \\\"**\\\" (SQLSTATE 42601)\"\n}\n", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/exec-3")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("{\n  \"Message\": \"Error: $sys.exec: first argument must be a SQL query (string)\"\n}\n", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/result-1")
	r.NotNil(resp)
	r.Equal(200, resp.StatusCode)
	r.Equal("foo", string(body))

	body, resp = doGet(r, "http://127.0.0.1:60000/result-2")
	r.NotNil(resp)
	r.Equal(500, resp.StatusCode)
	r.Equal("unsupported result type from script\n", string(body))

	s.Stop(time.Second)
}
