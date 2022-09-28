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
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

//------------------------------------------------------------------------------
// parameters

type paramInfo struct {
	rx   *regexp.Regexp // compiled "^{.Pattern}$"
	enum any            // []string, []int64 or []float64
}

func (a *APIServer) prepareParams() {
	for _, ep := range a.cfg.Endpoints {
		for _, p := range ep.Params {
			var info paramInfo

			// pattern
			if len(p.Pattern) > 0 {
				if rx, err := regexp.Compile("^" + p.Pattern + "$"); err == nil {
					info.rx = rx
				}
			}

			// enum
			if len(p.Enum) > 0 && (p.Type == "string" || p.Type == "integer" || p.Type == "number") {
				var sa []string
				var ia []int64
				var na []float64
				for _, v := range p.Enum {
					switch p.Type {
					case "string":
						if s, ok := v.(string); ok {
							sa = append(sa, s)
						}
					case "integer":
						if i, ok := v.(int64); ok {
							ia = append(ia, i)
						} else if i, ok := v.(uint64); ok {
							ia = append(ia, int64(i)) // checked to be <=math.MaxInt64
						} else if f, ok := v.(float64); ok {
							if i, ok := float2int(f); ok {
								ia = append(ia, i)
							}
						} else if s, ok := v.(string); ok {
							if i, err := strconv.ParseInt(s, 10, 64); err == nil {
								ia = append(ia, i)
							}
						}
					case "number":
						if i, ok := v.(int64); ok {
							na = append(na, float64(i))
						} else if i, ok := v.(uint64); ok {
							na = append(na, float64(i))
						} else if f, ok := v.(float64); ok {
							na = append(na, f)
						} else if s, ok := v.(string); ok {
							if f, err := strconv.ParseFloat(s, 64); err == nil {
								na = append(na, f)
							}
						}
					}
				}
				if len(sa) > 0 {
					info.enum = sa
				} else if len(ia) > 0 {
					info.enum = ia
				} else if len(na) > 0 {
					info.enum = na
				}
			} // enum

			if info.rx != nil || info.enum != nil {
				a.pinfo.Store(ep.URI+"#"+p.Name, &info)
			}

		} // for each param
	} // for each endpoint
}

func (a *APIServer) isSuitable(ep *Endpoint, p *Param, v any) (out any, err error) {
	// note: in case of query param or POST form body, v is always a []string
	var s string
	sv := false
	if sa, ok := v.([]string); ok && len(sa) == 1 {
		s = sa[0]
		sv = true
	} else {
		s, sv = v.(string)
	}

	switch p.Type {
	case "string":
		if sv {
			return a.checkString(ep, p, s)
		}
		return nil, errors.New("not a string")
	case "integer":
		if sv {
			return a.checkIntegerAny(ep, p, s)
		}
		return a.checkIntegerAny(ep, p, v)
	case "number":
		if sv {
			return a.checkFloatAny(ep, p, s)
		}
		return a.checkFloatAny(ep, p, v)
	case "boolean":
		if sv {
			return a.checkBoolAny(ep, p, s)
		}
		return a.checkBoolAny(ep, p, v)
	case "array":
		return a.checkArrayAny(ep, p, v)
	}

	// should not happen if valid cfg
	return nil, errors.New("unknown parameter type")
}

func (a *APIServer) checkStringAny(ep *Endpoint, p *Param, v any) (string, error) {
	if s, ok := v.(string); ok {
		return a.checkString(ep, p, s)
	}
	return "", fmt.Errorf("cannot convert value of type %T to string", v)
}

func (a *APIServer) checkString(ep *Endpoint, p *Param, s string) (string, error) {
	// enum
	if len(p.Enum) > 0 {
		if pi, ok := a.pinfo.Load(ep.URI + "#" + p.Name); ok && pi != nil {
			for _, v := range (pi.(*paramInfo)).enum.([]string) {
				if v == s {
					return s, nil
				}
			}
		}
		return "", errors.New("does not match any of the enumerated values")
	}

	// maxLength
	if p.MaxLength != nil && *p.MaxLength >= 0 && len(s) > *p.MaxLength {
		return "", fmt.Errorf("exceeds specified max length of %d", *p.MaxLength)
	}

	// pattern
	if len(p.Pattern) > 0 {
		if pi, ok := a.pinfo.Load(ep.URI + "#" + p.Name); ok && pi != nil {
			if rx := (pi.(*paramInfo)).rx; rx != nil {
				if !rx.MatchString(s) {
					return "", fmt.Errorf("does not match pattern %s", p.Pattern)
				}
			}
		}
	}

	return s, nil
}

func (a *APIServer) checkIntegerAny(ep *Endpoint, p *Param, v any) (int64, error) {
	if s, ok := v.(string); ok {
		// allow both "200.00" and "200"
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			if i, ok := float2int(f); ok {
				return a.checkInteger(ep, p, i)
			}
		}
		return 0, errors.New("not a valid integer")
	} else if f, ok := v.(float64); ok {
		if i, ok := float2int(f); ok {
			return a.checkInteger(ep, p, i)
		}
	}
	return 0, fmt.Errorf("cannot convert value of type %T to integer", v)
}

func (a *APIServer) checkInteger(ep *Endpoint, p *Param, i int64) (int64, error) {
	// enum
	if len(p.Enum) > 0 {
		if pi, ok := a.pinfo.Load(ep.URI + "#" + p.Name); ok && pi != nil {
			for _, v := range (pi.(*paramInfo)).enum.([]int64) {
				if v == i {
					return i, nil
				}
			}
		}
		return 0, errors.New("does not match any of the enumerated values")
	}

	// minimum
	if p.Minimum != nil {
		if min := int64(*p.Minimum); i < min {
			return 0, fmt.Errorf("is lower than the minimum of %d", min)
		}
	}

	// maximum
	if p.Maximum != nil {
		if max := int64(*p.Maximum); i > max {
			return 0, fmt.Errorf("is higher than the maximum of %d", max)
		}
	}

	return i, nil
}

func (a *APIServer) checkFloatAny(ep *Endpoint, p *Param, v any) (float64, error) {
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(s, 64); err != nil {
			return 0, errors.New("not a valid number")
		} else {
			return a.checkFloat(ep, p, f)
		}
	} else if f, ok := v.(float64); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
		return a.checkFloat(ep, p, f)
	}
	return 0, fmt.Errorf("cannot convert value of type %T to number", v)
}

func (a *APIServer) checkFloat(ep *Endpoint, p *Param, f float64) (float64, error) {
	// enum
	if len(p.Enum) > 0 {
		if pi, ok := a.pinfo.Load(ep.URI + "#" + p.Name); ok && pi != nil {
			for _, v := range (pi.(*paramInfo)).enum.([]float64) {
				if v == f {
					return f, nil
				}
			}
		}
		return 0, errors.New("does not match any of the enumerated values")
	}

	// minimum
	if p.Minimum != nil {
		if min := float64(*p.Minimum); f < min {
			return 0, fmt.Errorf("is lower than the minimum of %g", min)
		}
	}

	// maximum
	if p.Maximum != nil {
		if max := float64(*p.Maximum); f > max {
			return 0, fmt.Errorf("is higher than the maximum of %g", max)
		}
	}

	return f, nil
}

func float2int(f float64) (i int64, ok bool) {
	if i, frac := math.Modf(f); math.Abs(frac) < 1e-9 {
		return int64(i), true
	}
	return 0, false
}

func (a *APIServer) checkBoolAny(ep *Endpoint, p *Param, v any) (out bool, err error) {
	if s, ok := v.(string); ok {
		s = strings.ToLower(s)
		if s == "true" {
			return true, nil
		} else if s == "false" {
			return false, nil
		}
	} else if b, ok := v.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("cannot convert value of type %T to boolean", v)
}

func (a *APIServer) checkArrayAny(ep *Endpoint, p *Param, v any) (out any, err error) {
	if sa, ok := v.([]string); ok {
		aa := make([]any, len(sa))
		for i := range sa {
			aa[i] = sa[i]
		}
		return a.checkArray(ep, p, aa)
	} else if aa, ok := v.([]any); ok {
		return a.checkArray(ep, p, aa)
	}
	return nil, fmt.Errorf("cannot convert value of type %T to array", v)
}

func (a *APIServer) checkArray(ep *Endpoint, p *Param, v []any) (out any, err error) {
	// minItems
	if p.MinItems != nil && len(v) < *p.MinItems {
		return nil, fmt.Errorf("fewer than the specified minimum of %d items", *p.MinItems)
	}

	// maxItems
	if p.MaxItems != nil && len(v) > *p.MaxItems {
		return nil, fmt.Errorf("more than the specified maximum of %d items", *p.MaxItems)
	}

	// result is one of:
	var (
		sa []string
		ia []int64
		fa []float64
		ba []bool
	)

	// for each element:
	for j, ev := range v {
		switch p.ElemType {
		case "integer":
			if i, err := a.checkIntegerAny(ep, p, ev); err != nil {
				return nil, fmt.Errorf("enum value #%d: %v", j+1, err)
			} else {
				ia = append(ia, i)
			}
		case "number":
			if f, err := a.checkFloatAny(ep, p, ev); err != nil {
				return nil, fmt.Errorf("enum value #%d: %v", j+1, err)
			} else {
				fa = append(fa, f)
			}
		case "string":
			if s, err := a.checkStringAny(ep, p, ev); err != nil {
				return nil, fmt.Errorf("enum value #%d: %v", j+1, err)
			} else {
				sa = append(sa, s)
			}
		case "boolean":
			if b, err := a.checkBoolAny(ep, p, ev); err != nil {
				return nil, fmt.Errorf("enum value #%d: %v", j+1, err)
			} else {
				ba = append(ba, b)
			}
		}
	}

	// done, return appropriately
	switch p.ElemType {
	case "integer":
		return ia, nil
	case "number":
		return fa, nil
	case "string":
		return sa, nil
	case "boolean":
		return ba, nil
	}
	// should not happen for valid cfg
	return nil, fmt.Errorf("invalid elemType %q", p.ElemType)
}

func getCT(req *http.Request) (out string) {
	out = req.Header.Get("Content-Type")
	if pos := strings.IndexByte(out, ';'); pos > 0 {
		out = out[:pos]
	}
	return
}

func getJSON(req *http.Request, data any) error {
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, data)
}

func (a *APIServer) getParams(req *http.Request, ep *Endpoint,
	logger zerolog.Logger) ([]any, error) {

	var (
		jsonData map[string]any
		formData url.Values
		urlData  url.Values
	)
	if req.Method == "GET" {
		urlData = req.URL.Query()
	} else {
		var wrapped bool
		if ce := req.Header.Get("Content-Encoding"); ce == "gzip" {
			if r, err := gzip.NewReader(req.Body); err != nil {
				logger.Error().Err(err).Msg("failed to initialize gzip reader")
				return nil, fmt.Errorf("failed to initialize gzip reader: %v", err)
			} else {
				wrapped = true
				req.Body = r
			}
		} else if ce == "deflate" {
			wrapped = true
			req.Body = flate.NewReader(req.Body)
		}
		if ct := getCT(req); ct == "application/json" {
			if err := getJSON(req, &jsonData); err != nil {
				logger.Warn().Err(err).Msg("failed to decode json object in request body")
				jsonData = nil
			}
		} else if ct == "application/x-www-form-urlencoded" {
			if err := req.ParseForm(); err != nil {
				logger.Warn().Err(err).Msg("failed to parse form data in request body")
			} else {
				formData = req.PostForm
			}
		}
		if wrapped {
			if rc, ok := req.Body.(io.Closer); ok {
				if err := rc.Close(); err != nil {
					logger.Warn().Err(err).Msg("failed to close gzip/deflate reader")
				}
			}
		}
	}

	// discard (the rest of the) body, ignore errors, no need to close req.Body
	_, _ = io.CopyN(io.Discard, req.Body, 4096)

	getParam := func(in, key string) (v any, ok bool) {
		switch in {
		case "path":
			v = chi.URLParam(req, key)
			ok = v != ""
		case "query":
			v, ok = urlData[key]
		case "body":
			if jsonData != nil {
				v, ok = jsonData[key]
			} else if formData != nil {
				v, ok = formData[key]
			}
		}
		return
	}

	out := make([]any, len(ep.Params))
	for i := range ep.Params {
		p := &ep.Params[i]
		v, ok := getParam(p.In, p.Name)
		if !ok {
			if p.Required {
				logger.Error().Str("param", p.Name).Msg("value required but not supplied")
				return nil, fmt.Errorf("param %q: value required but not supplied", p.Name)
			} else {
				out[i] = nil
				continue
			}
		}
		// special case: boolean url/form parameters with no value will be considered
		// as true
		if p.Type == "boolean" &&
			(p.In == "query" || (p.In == "body" && jsonData == nil && formData != nil)) &&
			ok {
			if sa := v.([]string); len(sa) == 1 && len(sa[0]) == 0 {
				v = true
			}
		}
		if v2, err := a.isSuitable(ep, p, v); err != nil {
			logger.Error().Str("param", p.Name).Err(err).Msg("invalid value")
			return nil, fmt.Errorf("param %q: invalid value: %v", p.Name, err)
		} else {
			out[i] = v2
		}
	}

	return out, nil
}
