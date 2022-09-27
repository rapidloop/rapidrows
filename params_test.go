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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/rapidloop/rapidrows"
	"github.com/stretchr/testify/require"
)

func doGet(r *require.Assertions, u string) (body []byte, resp *http.Response) {
	var err error
	resp, err = http.Get(u)
	r.Nil(err)
	r.NotNil(resp)
	body, err = io.ReadAll(resp.Body)
	r.Nil(err)
	resp.Body.Close()
	return
}

func doPostForm(r *require.Assertions, u string, data url.Values) (body []byte, resp *http.Response) {
	var err error
	resp, err = http.PostForm(u, data)
	r.Nil(err)
	r.NotNil(resp)
	body, err = io.ReadAll(resp.Body)
	r.Nil(err)
	resp.Body.Close()
	return
}

func doPostJSON(r *require.Assertions, u string, data map[string]any) (body []byte, resp *http.Response) {
	var reqBody []byte
	var err error
	if data != nil {
		reqBody, err = json.Marshal(data)
		r.Nil(err)
	}
	resp, err = http.Post(u, "application/json; charset=utf-8", bytes.NewReader(reqBody))
	r.Nil(err)
	r.NotNil(resp)
	body, err = io.ReadAll(resp.Body)
	r.Nil(err)
	resp.Body.Close()
	return
}

func doAny(r *require.Assertions, u string, data ...any) (body []byte, resp *http.Response) {
	if len(data) == 0 {
		return doGet(r, u)
	} else if len(data) == 1 {
		if uv, ok := data[0].(url.Values); ok {
			return doPostForm(r, u, uv)
		} else if j, ok := data[0].(map[string]any); ok {
			return doPostJSON(r, u, j)
		}
	}
	panic("bad call to doAny")
}

func loadCfg(r *require.Assertions, s string) *rapidrows.APIServerConfig {
	var cfg rapidrows.APIServerConfig
	err := json.Unmarshal([]byte(s), &cfg)
	r.Nil(err)
	r.Nil(cfg.IsValid())
	return &cfg
}

func startServer(r *require.Assertions, cfg *rapidrows.APIServerConfig) *rapidrows.APIServer {
	s, err := rapidrows.NewAPIServer(cfg, nil)
	r.NotNil(s, "error was %v", err)
	r.Nil(err)
	r.Nil(s.Start())
	return s
}

func checkParamError(r *require.Assertions, u string, data ...any) {
	_, resp := doAny(r, u, data...)
	r.Equal(400, resp.StatusCode)
}

func checkParamNotFound(r *require.Assertions, u string, data ...any) {
	_, resp := doAny(r, u, data...)
	r.Equal(404, resp.StatusCode)
}

func checkParamOK(r *require.Assertions, u string, data ...any) {
	body, resp := doAny(r, u, data...)
	r.Equal(200, resp.StatusCode, "body was %q", string(body))
	r.Equal("success", string(body))
}

func mkptr[T any](v T) *T {
	return &v
}

const cfgTestParamsStr = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "static-text",
			"params": [
				{
					"name": "str",
					"in": "query",
					"type": "string",
					"pattern": "a+"
				}
			],
			"script": "success"
		}
	]
}`

func TestParamsStr(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsStr)

	// string, optional, in query, pattern=a+
	s := startServer(r, cfg)
	checkParamOK(r, "http://127.0.0.1:60000") // required=false
	checkParamError(r, "http://127.0.0.1:60000/?str=123")
	checkParamError(r, "http://127.0.0.1:60000/?str=12.3")
	checkParamError(r, "http://127.0.0.1:60000/?str=pqrs")
	checkParamOK(r, "http://127.0.0.1:60000/?str=aaa")
	s.Stop(time.Second * 5)

	// required=true
	cfg.Endpoints[0].Params[0].Required = true
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000")
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"str": []string{"aaa"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"str": "aaa"})
	checkParamOK(r, "http://127.0.0.1:60000/?str=aaa")
	s.Stop(time.Second * 5)

	// no pattern, maxlength=5, required
	cfg.Endpoints[0].Params[0].Pattern = ""
	cfg.Endpoints[0].Params[0].MaxLength = mkptr(5)
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?str=abcdefg")
	checkParamOK(r, "http://127.0.0.1:60000/?str=abcde")
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	s.Stop(time.Second * 5)

	// pattern=a+, maxlength=5, required
	cfg.Endpoints[0].Params[0].Pattern = "a+"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?str=abcdefg")
	checkParamError(r, "http://127.0.0.1:60000/?str=abcde")
	checkParamError(r, "http://127.0.0.1:60000/?str=aaaaaaa")
	checkParamOK(r, "http://127.0.0.1:60000/?str=aaaaa")
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	s.Stop(time.Second * 5)

	// pattern=a+ (ignored), maxlength=5 (ignored), enum, required
	cfg.Endpoints[0].Params[0].Pattern = "a+"
	cfg.Endpoints[0].Params[0].MaxLength = mkptr(5)
	cfg.Endpoints[0].Params[0].Enum = []any{"foo", "toolong"}
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?str=abcdefg")
	checkParamError(r, "http://127.0.0.1:60000/?str=abcde")
	checkParamError(r, "http://127.0.0.1:60000/?str=aaaaaaa")
	checkParamError(r, "http://127.0.0.1:60000/?str=aaaaa")
	checkParamOK(r, "http://127.0.0.1:60000/?str=foo")
	checkParamOK(r, "http://127.0.0.1:60000/?str=toolong")
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	s.Stop(time.Second * 5)

	// in path instead
	cfg.Endpoints[0].URI = "/{str}/foo"
	cfg.Endpoints[0].Params[0].In = "path"
	s = startServer(r, cfg)
	checkParamNotFound(r, "http://127.0.0.1:60000/?str=foo")
	checkParamError(r, "http://127.0.0.1:60000/x/foo")
	checkParamOK(r, "http://127.0.0.1:60000/foo/foo")
	checkParamOK(r, "http://127.0.0.1:60000/toolong/foo")
	checkParamError(r, "http://127.0.0.1:60000/aaaa/foo")
	checkParamError(r, "http://127.0.0.1:60000/x/foo",
		map[string]any{"str": "foo"})
	s.Stop(time.Second * 5)

	// in body instead
	cfg.Endpoints[0].URI = "/"
	cfg.Endpoints[0].Params[0].In = "body"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?str=foo")
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"str": []string{"foo"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"str": []string{"1.3"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"str": "foo"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"str": "aaaaa"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"str": 1.2})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"str": nil})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"str": true})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"str": map[string]any{}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"str": []any{}})
	s.Stop(time.Second * 5)
}

const cfgTestParamsInt = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "static-text",
			"params": [
				{
					"name": "int",
					"in": "query",
					"type": "integer",
					"minimum": 10,
					"maximum": 20
				}
			],
			"script": "success"
		}
	]
}`

const cfgTestParamsIntEnum = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "static-text",
			"params": [
				{
					"name": "int",
					"in": "query",
					"type": "integer",
					"required": true,
					"enum": [ 100, "200", 300.00 ]
				}
			],
			"script": "success"
		}
	]
}`

func TestParamsInt(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsInt)

	// integer "int", optional, in query, min=10, max=20
	s := startServer(r, cfg)
	checkParamOK(r, "http://127.0.0.1:60000") // required=false
	checkParamError(r, "http://127.0.0.1:60000/?int=12.3")
	checkParamError(r, "http://127.0.0.1:60000/?int=pqrs")
	checkParamError(r, "http://127.0.0.1:60000/?int=100")
	checkParamError(r, "http://127.0.0.1:60000/?int=8")
	checkParamOK(r, "http://127.0.0.1:60000/?int=15")
	s.Stop(time.Second * 5)

	// required=true
	cfg.Endpoints[0].Params[0].Required = true
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000")
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"int": []string{"15"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"int": 15})
	checkParamOK(r, "http://127.0.0.1:60000/?int=15")
	s.Stop(time.Second * 5)

	// no minimum, required
	cfg.Endpoints[0].Params[0].Minimum = nil
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?int=abcdefg")
	checkParamOK(r, "http://127.0.0.1:60000/?int=-4")
	checkParamOK(r, "http://127.0.0.1:60000/?int=19")
	checkParamOK(r, "http://127.0.0.1:60000/?int=20")     // max=20
	checkParamError(r, "http://127.0.0.1:60000/?int=190") // max=20
	checkParamError(r, "http://127.0.0.1:60000")          // required=true
	s.Stop(time.Second * 5)

	// max=20 (ignored), enum, required
	cfg.Endpoints[0].Params[0].Enum = []any{int64(100), int64(200)}
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?int=10")
	checkParamError(r, "http://127.0.0.1:60000/?int=hello")
	checkParamOK(r, "http://127.0.0.1:60000/?int=100")
	checkParamOK(r, "http://127.0.0.1:60000/?int=200")
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	s.Stop(time.Second * 5)

	// enum coming from json
	cfg = loadCfg(r, cfgTestParamsIntEnum)
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?int=10")
	checkParamError(r, "http://127.0.0.1:60000/?int=hello")
	checkParamOK(r, "http://127.0.0.1:60000/?int=100")
	checkParamOK(r, "http://127.0.0.1:60000/?int=200")
	checkParamOK(r, "http://127.0.0.1:60000/?int=300")
	checkParamError(r, "http://127.0.0.1:60000/?int=400")
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	s.Stop(time.Second * 5)

	// in path instead
	cfg.Endpoints[0].URI = "/{int}/foo"
	cfg.Endpoints[0].Params[0].In = "path"
	s = startServer(r, cfg)
	checkParamNotFound(r, "http://127.0.0.1:60000/?int=foo")
	checkParamError(r, "http://127.0.0.1:60000/x/foo")
	checkParamOK(r, "http://127.0.0.1:60000/100/foo")
	checkParamOK(r, "http://127.0.0.1:60000/200/foo")
	checkParamError(r, "http://127.0.0.1:60000/400/foo")
	checkParamError(r, "http://127.0.0.1:60000/x/foo",
		map[string]any{"int": 100})
	s.Stop(time.Second * 5)

	// in body instead
	cfg.Endpoints[0].URI = "/"
	cfg.Endpoints[0].Params[0].In = "body"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/")
	checkParamError(r, "http://127.0.0.1:60000/?int=100")
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"int": []string{"200.000"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"int": []string{"300"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"int": []string{"1.3"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"int": "300"})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"int": 300.00})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"int": 300})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"int": "aaaaa"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"int": 1.2})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"int": nil})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"int": true})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"int": map[string]any{}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"int": []any{}})
	s.Stop(time.Second * 5)
}

const cfgTestParamsNumber = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "static-text",
			"params": [
				{
					"name": "num",
					"in": "query",
					"type": "number",
					"minimum": 10,
					"maximum": 20
				}
			],
			"script": "success"
		}
	]
}`

const cfgTestParamsNumberEnum = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "static-text",
			"params": [
				{
					"name": "num",
					"in": "query",
					"type": "number",
					"required": true,
					"enum": [ 100.5, "200.5", 300.00 ]
				}
			],
			"script": "success"
		}
	]
}`

func TestParamsNumber(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsNumber)

	// number "num", optional, in query, min=10, max=20
	s := startServer(r, cfg)
	checkParamOK(r, "http://127.0.0.1:60000") // required=false
	checkParamOK(r, "http://127.0.0.1:60000/?num=12.3")
	checkParamError(r, "http://127.0.0.1:60000/?num=pqrs")
	checkParamError(r, "http://127.0.0.1:60000/?num=100")
	checkParamError(r, "http://127.0.0.1:60000/?num=8")
	checkParamOK(r, "http://127.0.0.1:60000/?num=15")
	s.Stop(time.Second * 5)

	// required=true
	cfg.Endpoints[0].Params[0].Required = true
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000")
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"num": []string{"15"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"num": 15})
	checkParamOK(r, "http://127.0.0.1:60000/?num=15.5")
	checkParamOK(r, "http://127.0.0.1:60000/?num=12")
	s.Stop(time.Second * 5)

	// no minimum, required
	cfg.Endpoints[0].Params[0].Minimum = nil
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?num=abcdefg")
	checkParamOK(r, "http://127.0.0.1:60000/?num=-4.4")
	checkParamOK(r, "http://127.0.0.1:60000/?num=19")
	checkParamOK(r, "http://127.0.0.1:60000/?num=19.5")
	checkParamOK(r, "http://127.0.0.1:60000/?num=20")     // max=20
	checkParamError(r, "http://127.0.0.1:60000/?num=190") // max=20
	checkParamError(r, "http://127.0.0.1:60000")          // required=true
	s.Stop(time.Second * 5)

	// max=20 (ignored), enum, required
	cfg.Endpoints[0].Params[0].Enum = []any{100.25, int64(200)}
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?num=10")
	checkParamError(r, "http://127.0.0.1:60000/?num=hello")
	checkParamError(r, "http://127.0.0.1:60000/?num=100")
	checkParamOK(r, "http://127.0.0.1:60000/?num=100.25")
	checkParamOK(r, "http://127.0.0.1:60000/?num=200")
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	s.Stop(time.Second * 5)

	// enum coming from json
	cfg = loadCfg(r, cfgTestParamsNumberEnum)
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/?num=10")
	checkParamError(r, "http://127.0.0.1:60000/?num=hello")
	checkParamOK(r, "http://127.0.0.1:60000/?num=100.5")
	checkParamOK(r, "http://127.0.0.1:60000/?num=200.5")
	checkParamOK(r, "http://127.0.0.1:60000/?num=300")
	checkParamError(r, "http://127.0.0.1:60000/?num=100")
	checkParamError(r, "http://127.0.0.1:60000/?num=101")
	checkParamError(r, "http://127.0.0.1:60000/?num=400")
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	s.Stop(time.Second * 5)

	// in path instead
	cfg.Endpoints[0].URI = "/{num}/foo"
	cfg.Endpoints[0].Params[0].In = "path"
	s = startServer(r, cfg)
	checkParamNotFound(r, "http://127.0.0.1:60000/?num=foo")
	checkParamError(r, "http://127.0.0.1:60000/x/foo")
	checkParamOK(r, "http://127.0.0.1:60000/100.5/foo")
	checkParamOK(r, "http://127.0.0.1:60000/200.5/foo")
	checkParamError(r, "http://127.0.0.1:60000/400/foo")
	checkParamError(r, "http://127.0.0.1:60000/x/foo",
		map[string]any{"num": 100.5})
	s.Stop(time.Second * 5)

	// in body instead
	cfg.Endpoints[0].URI = "/"
	cfg.Endpoints[0].Params[0].In = "body"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/")
	checkParamError(r, "http://127.0.0.1:60000/?num=100")
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"num": []string{"200.500"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"num": []string{"300.00000000000"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"num": []string{"1.3"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"num": "300"})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"num": 300.00})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"num": 300})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"num": "aaaaa"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"num": 1.2})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"num": nil})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"num": true})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"num": map[string]any{}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"num": []any{}})
	s.Stop(time.Second * 5)
}

const cfgTestParamsBoolean = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "static-text",
			"params": [
				{
					"name": "b",
					"in": "query",
					"type": "boolean"
				}
			],
			"script": "success"
		}
	]
}`

func TestParamsBoolean(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsBoolean)

	// boolean "b", optional, in query
	s := startServer(r, cfg)
	checkParamOK(r, "http://127.0.0.1:60000") // required=false
	checkParamOK(r, "http://127.0.0.1:60000/?b=true")
	checkParamOK(r, "http://127.0.0.1:60000/?b=false")
	checkParamOK(r, "http://127.0.0.1:60000/?b")
	checkParamError(r, "http://127.0.0.1:60000/?b=100")
	s.Stop(time.Second * 5)

	// required=true
	cfg.Endpoints[0].Params[0].Required = true
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000")
	checkParamOK(r, "http://127.0.0.1:60000/?b=true")
	checkParamOK(r, "http://127.0.0.1:60000/?b=false")
	checkParamOK(r, "http://127.0.0.1:60000/?b")
	checkParamError(r, "http://127.0.0.1:60000/?b=100")
	s.Stop(time.Second * 5)

	// in path instead
	cfg.Endpoints[0].URI = "/foo/{b}"
	cfg.Endpoints[0].Params[0].In = "path"
	s = startServer(r, cfg)
	checkParamNotFound(r, "http://127.0.0.1:60000/?b=true")
	checkParamNotFound(r, "http://127.0.0.1:60000/foo")
	checkParamOK(r, "http://127.0.0.1:60000/foo/true")
	checkParamOK(r, "http://127.0.0.1:60000/foo/false")
	checkParamNotFound(r, "http://127.0.0.1:60000/foo",
		map[string]any{"b": 100.5})
	s.Stop(time.Second * 5)

	// in body instead
	cfg.Endpoints[0].URI = "/"
	cfg.Endpoints[0].Params[0].In = "body"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/")
	checkParamError(r, "http://127.0.0.1:60000/?b=true")
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"b": []string{""}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"b": []string{"true"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"b": []string{"false"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"b": []string{"100"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"b": true})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"b": false})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"b": 300.00})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"b": 300})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"b": "aaaaa"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"b": nil})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"b": map[string]any{}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"b": []any{}})
	s.Stop(time.Second * 5)
}

const cfgTestParamsArray = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "static-text",
			"params": [
				{
					"name": "a",
					"in": "query",
					"type": "array",
					"elemType": "integer",
					"minItems": 3,
					"maxItems": 4
				}
			],
			"script": "success"
		}
	]
}`

func TestParamsArrayInteger(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsArray)

	// array "a", optional, in query
	s := startServer(r, cfg)
	checkParamOK(r, "http://127.0.0.1:60000") // required=false
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20")
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30&a=40")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30&a=40&a=50")
	checkParamError(r, "http://127.0.0.1:60000/?a=false")
	checkParamError(r, "http://127.0.0.1:60000/?a")
	checkParamError(r, "http://127.0.0.1:60000/?a=true")
	checkParamError(r, "http://127.0.0.1:60000/?a=100.5&a=200.5&a=300.5")
	s.Stop(time.Second * 5)

	// required=true
	cfg.Endpoints[0].Params[0].Required = true
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")
	checkParamError(r, "http://127.0.0.1:60000/?a=false")
	checkParamError(r, "http://127.0.0.1:60000/?a")
	checkParamError(r, "http://127.0.0.1:60000/?a=true")
	checkParamError(r, "http://127.0.0.1:60000/?a=100.5&a=200.5&a=300.5")
	s.Stop(time.Second * 5)

	// in body instead
	cfg.Endpoints[0].URI = "/"
	cfg.Endpoints[0].Params[0].In = "body"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")

	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3", "4"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3", "4", "5"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"true"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1.5", "2.5", "3.5"}}))

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3, 4}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3, 4, 5}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []float64{1.5, 2.5, 3.5}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []float64{10, 20, 30}})

	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"1", "2", "3"}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"1.5", "2.5", "3.5"}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"a", "b", "c"}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []bool{true, true, true}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []any{nil, map[string]any{}, []any{}}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": false})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": 300.00})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": 300})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": "aaaaa"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": nil})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": map[string]any{}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []any{}})
	s.Stop(time.Second * 5)
}

func TestParamsArrayNumber(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsArray)
	cfg.Endpoints[0].Params[0].ElemType = "number"

	// array "a", optional, in query
	s := startServer(r, cfg)
	checkParamOK(r, "http://127.0.0.1:60000") // required=false
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20")
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")
	checkParamOK(r, "http://127.0.0.1:60000/?a=10.5&a=20.5&a=30.5")
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30&a=40")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30&a=40&a=50")
	checkParamError(r, "http://127.0.0.1:60000/?a=false")
	checkParamError(r, "http://127.0.0.1:60000/?a")
	checkParamError(r, "http://127.0.0.1:60000/?a=true")
	checkParamOK(r, "http://127.0.0.1:60000/?a=100.5&a=200.5&a=300.5")
	s.Stop(time.Second * 5)

	// required=true
	cfg.Endpoints[0].Params[0].Required = true
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")
	checkParamError(r, "http://127.0.0.1:60000/?a=false")
	checkParamError(r, "http://127.0.0.1:60000/?a")
	checkParamError(r, "http://127.0.0.1:60000/?a=true")
	checkParamOK(r, "http://127.0.0.1:60000/?a=100.5&a=200.5&a=300.5")
	s.Stop(time.Second * 5)

	// in body instead
	cfg.Endpoints[0].URI = "/"
	cfg.Endpoints[0].Params[0].In = "body"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")

	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3", "4"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3", "4", "5"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"true"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1.5", "2.5", "3.5"}}))

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3, 4}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3, 4, 5}})

	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []float64{1.5, 2.5, 3.5}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []float64{10, 20, 30}})

	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"1", "2", "3"}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"1.5", "2.5", "3.5"}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"a", "b", "c"}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []bool{true, true, true}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []any{nil, map[string]any{}, []any{}}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": false})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": 300.00})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": 300})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": "aaaaa"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": nil})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": map[string]any{}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []any{}})
	s.Stop(time.Second * 5)
}

func TestParamsArrayString(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsArray)
	cfg.Endpoints[0].Params[0].ElemType = "string"

	// array "a", optional, in query
	s := startServer(r, cfg)
	checkParamOK(r, "http://127.0.0.1:60000") // required=false
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20")
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")
	checkParamOK(r, "http://127.0.0.1:60000/?a=10.5&a=20.5&a=30.5")
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30&a=40")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30&a=40&a=50")
	checkParamError(r, "http://127.0.0.1:60000/?a=false")
	checkParamError(r, "http://127.0.0.1:60000/?a")
	checkParamError(r, "http://127.0.0.1:60000/?a=true")
	checkParamOK(r, "http://127.0.0.1:60000/?a=100.5&a=200.5&a=300.5")
	s.Stop(time.Second * 5)

	// required=true
	cfg.Endpoints[0].Params[0].Required = true
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	checkParamOK(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")
	checkParamError(r, "http://127.0.0.1:60000/?a=false")
	checkParamError(r, "http://127.0.0.1:60000/?a")
	checkParamError(r, "http://127.0.0.1:60000/?a=true")
	checkParamOK(r, "http://127.0.0.1:60000/?a=100.5&a=200.5&a=300.5")
	s.Stop(time.Second * 5)

	// in body instead
	cfg.Endpoints[0].URI = "/"
	cfg.Endpoints[0].Params[0].In = "body"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")

	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3", "4"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3", "4", "5"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"true"}}))
	checkParamOK(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1.5", "2.5", "3.5"}}))

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3, 4}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3, 4, 5}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []float64{1.5, 2.5, 3.5}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []float64{10, 20, 30}})

	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"1", "2", "3"}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"1.5", "2.5", "3.5"}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"a", "b", "c"}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []bool{true, true, true}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []any{nil, map[string]any{}, []any{}}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": false})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": 300.00})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": 300})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": "aaaaa"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": nil})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": map[string]any{}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []any{}})
	s.Stop(time.Second * 5)
}

func TestParamsArrayBoolean(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsArray)
	cfg.Endpoints[0].Params[0].ElemType = "boolean"

	// array "a", optional, in query
	s := startServer(r, cfg)
	checkParamOK(r, "http://127.0.0.1:60000") // required=false
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")
	checkParamOK(r, "http://127.0.0.1:60000/?a=true&a=false&a=true")
	checkParamOK(r, "http://127.0.0.1:60000/?a=true&a=false&a=true&a=false")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30&a=40&a=50")
	checkParamError(r, "http://127.0.0.1:60000/?a=false")
	checkParamError(r, "http://127.0.0.1:60000/?a")
	checkParamError(r, "http://127.0.0.1:60000/?a=true")
	checkParamError(r, "http://127.0.0.1:60000/?a=100.5&a=200.5&a=300.5")
	s.Stop(time.Second * 5)

	// required=true
	cfg.Endpoints[0].Params[0].Required = true
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000") // required=true
	s.Stop(time.Second * 5)

	// in body instead
	cfg.Endpoints[0].URI = "/"
	cfg.Endpoints[0].Params[0].In = "body"
	s = startServer(r, cfg)
	checkParamError(r, "http://127.0.0.1:60000/")
	checkParamError(r, "http://127.0.0.1:60000/?a=10&a=20&a=30")

	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3", "4"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1", "2", "3", "4", "5"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"true"}}))
	checkParamError(r, "http://127.0.0.1:60000/",
		url.Values(map[string][]string{"a": []string{"1.5", "2.5", "3.5"}}))

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3, 4}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []int64{1, 2, 3, 4, 5}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []float64{1.5, 2.5, 3.5}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []float64{10, 20, 30}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"1", "2", "3"}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"1.5", "2.5", "3.5"}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []string{"a", "b", "c"}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []bool{true, true}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []bool{true, true, true}})
	checkParamOK(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []bool{true, true, false, true}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []bool{true, true, false, true, true}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []any{nil, map[string]any{}, []any{}}})

	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": false})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": 300.00})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": 300})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": "aaaaa"})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": nil})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": map[string]any{}})
	checkParamError(r, "http://127.0.0.1:60000/",
		map[string]any{"a": []any{}})
	s.Stop(time.Second * 5)
}

const cfgTestParamsInvalidBody = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/",
			"implType": "static-text",
			"params": [
				{
					"name": "str",
					"in": "body",
					"type": "string",
					"required": true
				}
			],
			"script": "success"
		}
	]
}`

func TestParamsInvalidJSON(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsInvalidBody)
	s := startServer(r, cfg)
	time.Sleep(500 * time.Millisecond)
	resp, err := http.Post("http://127.0.0.1:60000/",
		"application/json; charset=utf-8", bytes.NewReader([]byte{'*', '*', '*'}))
	r.Nil(err, "error was %v", err)
	r.NotNil(resp)
	_, err = io.ReadAll(resp.Body)
	r.Nil(err)
	resp.Body.Close()
	r.Equal(400, resp.StatusCode)
	s.Stop(time.Second * 5)
}

func TestParamsEmptyJSON(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsInvalidBody)
	s := startServer(r, cfg)
	time.Sleep(500 * time.Millisecond)
	resp, err := http.Post("http://127.0.0.1:60000/",
		"application/json; charset=utf-8", bytes.NewReader([]byte{}))
	r.Nil(err, "error was %v", err)
	r.NotNil(resp)
	_, err = io.ReadAll(resp.Body)
	r.Nil(err)
	resp.Body.Close()
	r.Equal(400, resp.StatusCode)
	s.Stop(time.Second * 5)
}

func TestParamsInvalidPost(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestParamsInvalidBody)
	s := startServer(r, cfg)
	time.Sleep(500 * time.Millisecond)
	resp, err := http.Post("http://127.0.0.1:60000/",
		"application/x-www-form-urlencoded", bytes.NewReader([]byte{';', ';'}))
	r.Nil(err, "error was %v", err)
	r.NotNil(resp)
	_, err = io.ReadAll(resp.Body)
	r.Nil(err)
	resp.Body.Close()
	r.Equal(400, resp.StatusCode)
	s.Stop(time.Second * 5)
}
