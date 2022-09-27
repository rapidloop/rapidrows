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
	"bufio"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"nhooyr.io/websocket"
)

const cfgTestStreamsBasic = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/notify/{pgchan}/{payload}",
			"implType": "exec",
			"script": "select pg_notify($1, $2)",
			"datasource": "default",
			"params": [
				{
					"name": "pgchan",
					"in": "path",
					"type": "string"
				},
				{
					"name": "payload",
					"in": "path",
					"type": "string"
				}
			]
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
		},
		{
			"uri": "/sse",
			"type": "sse",
			"channel": "chansse",
			"datasource": "default",
			"debug": true
		},
		{
			"uri": "/ws",
			"type": "websocket",
			"channel": "chanws",
			"datasource": "default"
		}
	],
	"datasources": [ { "name": "default" } ]
}`

const expResultSSE = `:

data: foo

data: bar

`

func TestStreamsSSE(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestStreamsBasic)
	s := startServerFull(r, cfg)

	resp, err := http.Get("http://127.0.0.1:60000/sse")
	r.Nil(err)
	r.NotNil(resp)
	r.Equal(200, resp.StatusCode)
	r.NotNil(resp.Body)

	_, resp2 := doGet(r, "http://127.0.0.1:60000/notify/chansse/foo")
	r.Equal(200, resp2.StatusCode)
	_, resp2 = doGet(r, "http://127.0.0.1:60000/notify/chansse/bar")
	r.Equal(200, resp2.StatusCode)

	var result string
	scanner := bufio.NewScanner(resp.Body)
	n := 1
	for n <= 6 && scanner.Scan() {
		result += scanner.Text() + "\n"
		n++
	}
	r.Equal(expResultSSE, result)

	s.Stop(time.Second)
}

func TestStreamsWS(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestStreamsBasic)
	s := startServerFull(r, cfg)

	conn, resp, err := websocket.Dial(context.Background(), "ws://127.0.0.1:60000/ws", nil)
	r.Nil(err)
	r.Equal(101, resp.StatusCode)
	r.NotNil(conn)

	_, resp2 := doGet(r, "http://127.0.0.1:60000/notify/chanws/foo")
	r.Equal(200, resp2.StatusCode)
	_, resp2 = doGet(r, "http://127.0.0.1:60000/notify/chanws/bar")
	r.Equal(200, resp2.StatusCode)

	mt, data, err := conn.Read(context.Background())
	r.Nil(err)
	r.Equal(websocket.MessageText, mt)
	r.Equal("foo", string(data))

	mt, data, err = conn.Read(context.Background())
	r.Nil(err)
	r.Equal(websocket.MessageText, mt)
	r.Equal("bar", string(data))

	conn.Close(websocket.StatusInternalError, "bye")

	s.Stop(time.Second)
}

// TestStreamsWSNoWrite tests whether it is possible to send messages from the
// client to the server (it is not).
func TestStreamsWSNoWrite(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestStreamsBasic)
	s := startServerFull(r, cfg)

	conn, resp, err := websocket.Dial(context.Background(), "ws://127.0.0.1:60000/ws", nil)
	r.Nil(err)
	r.Equal(101, resp.StatusCode)
	r.NotNil(conn)

	err = conn.Write(context.Background(), websocket.MessageText, []byte("baz"))
	if err != nil {
		r.EqualError(err, `failed to get reader: received close frame: status = StatusPolicyViolation and reason = "unexpected data message"`)
	} else {
		_, _, err := conn.Read(context.Background())
		r.EqualError(err, `failed to get reader: received close frame: status = StatusPolicyViolation and reason = "unexpected data message"`)
	}

	conn.Close(websocket.StatusInternalError, "bye")

	s.Stop(time.Second)
}

func TestStreamsBadClients(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestStreamsBasic)
	s := startServerFull(r, cfg)

	for i := 0; i < 20; i++ {
		_, resp2 := doGet(r, "http://127.0.0.1:60000/notify/chanws/foo")
		r.Equal(200, resp2.StatusCode)
		_, resp2 = doGet(r, "http://127.0.0.1:60000/notify/chansse/foo")
		r.Equal(200, resp2.StatusCode)
	}

	for i := 0; i < 10; i++ {
		go func() {
			conn, resp, err := websocket.Dial(context.Background(), "ws://127.0.0.1:60000/ws", nil)
			r.Nil(err)
			r.Equal(101, resp.StatusCode)
			r.NotNil(conn)
			conn.Close(websocket.StatusInternalError, "bye")
		}()

		go func() {
			conn, resp, err := websocket.Dial(context.Background(), "ws://127.0.0.1:60000/ws", nil)
			r.Nil(err)
			r.Equal(101, resp.StatusCode)
			r.NotNil(conn)
			conn.Write(context.Background(), websocket.MessageText, []byte("baz"))
		}()

		go func() {
			resp, err := http.Get("http://127.0.0.1:60000/sse")
			r.Nil(err)
			r.NotNil(resp)
			r.Equal(200, resp.StatusCode)
			r.NotNil(resp.Body)
			resp.Body.Close()
		}()
	}

	for i := 0; i < 20; i++ {
		_, resp2 := doGet(r, "http://127.0.0.1:60000/notify/chanws/foo")
		r.Equal(200, resp2.StatusCode)
		_, resp2 = doGet(r, "http://127.0.0.1:60000/notify/chansse/foo")
		r.Equal(200, resp2.StatusCode)
	}

	time.Sleep(time.Second)
	s.Stop(time.Second)
}
