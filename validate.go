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
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"
	"golang.org/x/mod/semver"
)

//------------------------------------------------------------------------------

func addWarn(r []ValidationResult, msg string) []ValidationResult {
	return append(r, ValidationResult{
		Warn:    true,
		Message: msg,
	})
}

func addError(r []ValidationResult, msg string) []ValidationResult {
	return append(r, ValidationResult{
		Warn:    false,
		Message: msg,
	})
}

//------------------------------------------------------------------------------
// server

var (
	rxPort   = regexp.MustCompile(`:[0-9]+$`)
	rxPrefix = regexp.MustCompile(`^(/[A-Za-z0-9_.-]+)+$`)
)

func (c *APIServerConfig) validate() (r []ValidationResult) {
	// Version
	if !semver.IsValid("v" + c.Version) {
		r = addError(r, fmt.Sprintf("invalid schema version %q: must be semver", c.Version))
	} else if semver.Canonical("v"+c.Version) != "v1.0.0" {
		r = addError(r, fmt.Sprintf("incompatible schema version %q", c.Version))
	}
	// Listen
	if len(c.Listen) > 0 {
		l := c.Listen
		if !rxPort.MatchString(c.Listen) {
			l += ":8080"
		}
		if host, port, err := net.SplitHostPort(l); err != nil {
			r = addError(r, fmt.Sprintf("invalid listen specification %q", c.Listen))
		} else if nport, err := strconv.Atoi(port); err != nil || nport <= 0 || nport >= 65535 {
			r = addError(r, fmt.Sprintf("invalid listen specification: bad port %q", port))
		} else if host != "" && net.ParseIP(host) == nil {
			r = addError(r, fmt.Sprintf("invalid listen specification: bad IP %q", host))
		}
	}
	// CommonPrefix
	if len(c.CommonPrefix) > 0 {
		if !rxPrefix.MatchString(c.CommonPrefix) {
			r = addError(r, fmt.Sprintf("invalid common prefix %q", c.CommonPrefix))
		}
	}
	// CORS
	if c.CORS != nil {
		r = append(r, c.CORS.validate()...)
	}
	// Endpoints
	epURIs := make(map[string]int)
	for i := range c.Endpoints {
		epURIs[c.Endpoints[i].URI] += 1
		r = append(r, c.Endpoints[i].validate(c.Datasources)...)
	}
	// check uniqueness of endpoint URIs
	for u, c := range epURIs {
		if c > 1 {
			r = addError(r, fmt.Sprintf("%d endpoints with same URI %q",
				c, u))
		}
	}
	// Streams
	sURIs := make(map[string]int)
	for i := range c.Streams {
		sURIs[c.Streams[i].URI] += 1
		r = append(r, c.Streams[i].validate(c.Datasources)...)
	}
	// check uniqueness of stream URIs
	for u, c := range sURIs {
		if c > 1 {
			r = addError(r, fmt.Sprintf("%d streams with same URI %q",
				c, u))
		}
	}
	// check uniqueness of URIs of streams+endpoints
	for u, epc := range epURIs {
		if sc := sURIs[u]; sc > 0 {
			r = addError(r, fmt.Sprintf("%d endpoint and %d stream with same URI %q",
				epc, sc, u))
		}
	}
	// Jobs
	jobNames := make(map[string]int)
	for i := range c.Jobs {
		jobNames[c.Jobs[i].Name] += 1
		r = append(r, c.Jobs[i].validate(c.Datasources)...)
	}
	// check uniqueness of job names
	for n, c := range jobNames {
		if c > 1 {
			r = addError(r, fmt.Sprintf("%d jobs named %q", c, n))
		}
	}
	// Datasources
	dsNames := make(map[string]int)
	for i := range c.Datasources {
		dsNames[c.Datasources[i].Name] += 1
		r = append(r, c.Datasources[i].validate()...)
	}
	// check uniqueness of datasource names
	for n, c := range dsNames {
		if c > 1 {
			r = addError(r, fmt.Sprintf("%d datasources named %q", c, n))
		}
	}
	return
}

//------------------------------------------------------------------------------
// server -> cors

func (c *CORS) validate() (r []ValidationResult) {
	// AllowedOrigins
	for _, o := range c.AllowedOrigins {
		if n := strings.Count(o, "*"); n > 1 {
			r = addError(r, fmt.Sprintf("cors: allowed origin %q: can use only 1 wildcard",
				o))
		}
	}
	// AllowedMethods
	for _, m := range c.AllowedMethods {
		if !rxMethod.MatchString(m) {
			r = addError(r, fmt.Sprintf("cors: allowed methods: invalid method %q",
				m))
		}
	}
	// TODO: AllowedHeaders & ExposedHeaders: check if all elements are valid
	// for use an HTTP header key
	// MaxAge
	if c.MaxAge != nil && *c.MaxAge <= 0 {
		r = addWarn(r, fmt.Sprintf("cors: max age %d is <=0, will be ignored",
			*c.MaxAge))
	}
	return
}

//------------------------------------------------------------------------------
// endpoint

var (
	rxURI    = regexp.MustCompile(`^(/(({[A-Za-z0-9_.-]+})|([A-Za-z0-9_.-]+)))+$`)
	rxMethod = regexp.MustCompile(`^((GET)|(POST)|(PUT)|(PATCH)|(DELETE))$`)
)

func (ep *Endpoint) validate(ds []Datasource) (r []ValidationResult) {
	// URI
	if !rxURI.MatchString(ep.URI) && ep.URI != "/" {
		r = addError(r, fmt.Sprintf("endpoint %q: invalid URI", ep.URI))
	}
	// Methods
	for i, m := range ep.Methods {
		if !rxMethod.MatchString(m) {
			r = addError(r, fmt.Sprintf("endpoint %q: method #%d: invalid method %q",
				ep.URI, i+1, m))
		}
	}
	// Params
	paramNames := make(map[string]int)
	for i := range ep.Params {
		paramNames[ep.Params[i].Name] += 1
		r = append(r, ep.Params[i].validate(ep.URI)...)
	}
	// check uniqueness of param names
	for n, c := range paramNames {
		if c > 1 {
			r = addError(r, fmt.Sprintf("endpoint %q: %d params named %q",
				ep.URI, c, n))
		}
	}
	// ImplType
	if ep.ImplType != "query-json" && ep.ImplType != "query-csv" &&
		ep.ImplType != "exec" && ep.ImplType != "static-text" &&
		ep.ImplType != "static-json" && ep.ImplType != "javascript" {
		r = addError(r, fmt.Sprintf("endpoint %q: invalid implementation type %q",
			ep.URI, ep.ImplType))
	}
	// Datasource
	if ep.ImplType == "query-json" || ep.ImplType == "query-csv" ||
		ep.ImplType == "exec" {
		found := false
		for i := range ds {
			if ds[i].Name == ep.Datasource {
				found = true
				break
			}
		}
		if !found {
			r = addError(r, fmt.Sprintf("endpoint %q: unknown datasource %q",
				ep.URI, ep.Datasource))
		}
	}
	// Script
	if len(strings.TrimSpace(ep.Script)) == 0 && ep.ImplType != "static-text" {
		r = addError(r, fmt.Sprintf("endpoint %q: invalid script: empty",
			ep.URI))
	}
	if ep.ImplType == "static-json" && !json.Valid([]byte(ep.Script)) {
		r = addError(r, fmt.Sprintf("endpoint %q: invalid script: invalid json",
			ep.URI))
	}
	// TxOptions
	if ep.TxOptions != nil {
		r = append(r, ep.TxOptions.validate(fmt.Sprintf("endpoint %q:", ep.URI))...)
	}
	// Timeout
	if ep.Timeout != nil && *ep.Timeout <= 0 {
		r = addWarn(r, fmt.Sprintf("endpoint %q: timeout %g is <=0, will be ignored",
			ep.URI, *ep.Timeout))
	}
	// Cache
	if ep.Cache != nil && *ep.Cache <= 0 {
		r = addWarn(r, fmt.Sprintf("endpoint %q: cache ttl %g is <=0, will be ignored",
			ep.URI, *ep.Cache))
	}
	return
}

//------------------------------------------------------------------------------
// endpoint -> txoptions

func (tx *TxOptions) validate(pfx string) (r []ValidationResult) {
	// Access (empty = read write)
	access := strings.ToLower(tx.Access)
	if access != "read only" && access != "read write" && access != "" {
		r = addError(r, fmt.Sprintf("%s invalid access specifier %q",
			pfx, tx.Access))
	}
	// ISOLevel (empty = read committed)
	isoLevel := strings.ToLower(tx.ISOLevel)
	if isoLevel != "read committed" && isoLevel != "repeatable read" &&
		isoLevel != "serializable" && isoLevel != "" {
		r = addError(r, fmt.Sprintf("%s invalid iso level %q",
			pfx, tx.ISOLevel))
	}
	return
}

//------------------------------------------------------------------------------
// endpoint -> param

var rxParamName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func (p *Param) validate(u string) (r []ValidationResult) {
	pfx := fmt.Sprintf("endpoint %q: param %q:", u, p.Name)
	isint := func(v any) (ok bool) { _, ok = v.(int64); return }
	isuint := func(v any) (ok bool) { _, ok = v.(uint64); return }
	isfloat := func(v any) (ok bool) { _, ok = v.(float64); return }
	isstring := func(v any) (ok bool) { _, ok = v.(string); return }

	// Name
	if !rxParamName.MatchString(p.Name) {
		r = addError(r, fmt.Sprintf("%s invalid name", pfx))
	}
	// In
	if p.In != "query" && p.In != "path" && p.In != "body" {
		r = addError(r, fmt.Sprintf("%s invalid location %q", pfx, p.In))
	}
	// Type
	if p.Type != "integer" && p.Type != "number" && p.Type != "string" &&
		p.Type != "boolean" && p.Type != "array" {
		r = addError(r, fmt.Sprintf("%s invalid type %q", pfx, p.Type))
	}
	// if type is 'array', disallow in = 'path'
	if p.Type == "array" && p.In == "path" {
		r = addError(r, fmt.Sprintf("%s type 'array' cannot occur in 'path'", pfx))
	}
	// Enum
	if len(p.Enum) > 0 {
		//	- type must be integer or number or string
		if p.Type != "integer" && p.Type != "number" && p.Type != "string" {
			r = addError(r,
				fmt.Sprintf("%s enum cannot be specified for parameter of type %q",
					pfx, p.Type))
		}
		//	- elements must match type
		for _, v := range p.Enum {
			switch p.Type {
			case "string":
				if !isstring(v) {
					r = addError(r, fmt.Sprintf("%s enum entry '%v': invalid string",
						pfx, v))
				}
			case "integer":
				if isstring(v) {
					if _, err := strconv.ParseInt(v.(string), 10, 64); err != nil {
						// string not convertible to a valid integer
						r = addError(r, fmt.Sprintf("%s enum entry %q: not a valid integer",
							pfx, v.(string)))
					}
				} else if isfloat(v) {
					if _, ok := float2int(v.(float64)); !ok {
						// has fractional part
						r = addError(r, fmt.Sprintf("%s enum entry '%v': not a valid integer (has fractional part)",
							pfx, v))
					}
				} else if isuint(v) {
					if v.(uint64) > math.MaxInt64 {
						// value too big
						r = addError(r, fmt.Sprintf("%s enum entry '%v': not a valid integer (value too large)",
							pfx, v))
					}
				} else if !isint(v) {
					// can't be used as an int
					r = addError(r, fmt.Sprintf("%s enum entry '%v': not a valid integer",
						pfx, v))
				}
			case "number":
				if isstring(v) {
					if _, err := strconv.ParseFloat(v.(string), 64); err != nil {
						// string not convertible to a valid number
						r = addError(r, fmt.Sprintf("%s enum entry %q: not a valid number",
							pfx, v.(string)))
					}
				} else if !isuint(v) && !isint(v) && !isfloat(v) {
					// can't be used as a number
					r = addError(r, fmt.Sprintf("%s enum entry '%v': not a valid number",
						pfx, v))
				}
			}
		}
	}
	// Minimum
	if p.Minimum != nil {
		//	- type must be integer or number
		if p.Type != "integer" && p.Type != "number" {
			r = addError(r, fmt.Sprintf("%s minimum can be specified only for params of type integer or number",
				pfx))
		}
		//	- frac. part must be 0 if type is integer
		if p.Type == "integer" {
			if _, ok := float2int(*p.Minimum); !ok {
				// has fractional part
				r = addError(r, fmt.Sprintf("%s minimum %v not a valid integer (has fractional part)",
					pfx, *p.Minimum))
			}
		}
	}
	// Maximum
	if p.Maximum != nil {
		//	- type must be integer or number
		if p.Type != "integer" && p.Type != "number" {
			r = addError(r, fmt.Sprintf("%s maximum can be specified only for params of type integer or number",
				pfx))
		}
		//	- frac. part must be 0 if type is integer
		if p.Type == "integer" {
			if _, ok := float2int(*p.Maximum); !ok {
				// has fractional part
				r = addError(r, fmt.Sprintf("%s maximum %v not a valid integer (has fractional part)",
					pfx, *p.Maximum))
			}
		}
		//	- must be >= minimum if both are specified
		if p.Minimum != nil {
			if *p.Maximum < *p.Minimum {
				r = addError(r, fmt.Sprintf("%s maximum %v is less than minimum %v",
					pfx, *p.Maximum, *p.Minimum))
			}
		}
	}
	// MaxLength
	if p.MaxLength != nil {
		//	- type must be string
		if p.Type != "string" {
			r = addError(r, fmt.Sprintf("%s maxLength can be specified only for params of type string",
				pfx))
		}
		//	- must be >= 0
		if *p.MaxLength < 0 {
			r = addError(r, fmt.Sprintf("%s maxLength %d should be >= 0", pfx,
				*p.MaxLength))
		}
	}
	// Pattern
	if len(p.Pattern) > 0 {
		//	- type must be string
		if p.Type != "string" {
			r = addError(r, fmt.Sprintf("%s pattern can be specified only for params of type string",
				pfx))
		}
		//	- must be a valid regexp
		if _, err := regexp.Compile("^" + p.Pattern + "$"); err != nil {
			r = addError(r, fmt.Sprintf("%s pattern is not a valid unanchored regex", pfx))
		}
	}
	// MinItems
	if p.MinItems != nil {
		//	- type must be array
		if p.Type != "array" {
			r = addError(r, fmt.Sprintf("%s minItems can be specified only for params of type array",
				pfx))
		}
		//	- must be >= 0
		if *p.MinItems < 0 {
			r = addError(r, fmt.Sprintf("%s minItems %d should be >= 0", pfx,
				*p.MinItems))
		}
	}
	// MaxItems
	if p.MaxItems != nil {
		//	- type must be array
		if p.Type != "array" {
			r = addError(r, fmt.Sprintf("%s maxItems can be specified only for params of type array",
				pfx))
		}
		//	- must be >= 0
		if *p.MaxItems < 0 {
			r = addError(r, fmt.Sprintf("%s maxItems %d should be >= 0", pfx,
				*p.MaxItems))
		}
		//	- must be >= minItems if both are specified
		if p.MinItems != nil {
			if *p.MaxItems < *p.MinItems {
				r = addError(r, fmt.Sprintf("%s maxItems %v is less than minItems %v",
					pfx, *p.MaxItems, *p.MinItems))
			}
		}
	}
	// ElemType
	//	- type must be array
	if len(p.ElemType) > 0 && p.Type != "array" {
		r = addError(r, fmt.Sprintf("%s elemType can be specified only for params of type array",
			pfx))
	}
	if len(p.ElemType) == 0 && p.Type == "array" {
		r = addError(r, fmt.Sprintf("%s elemType must be specified for params of type array",
			pfx))
	}
	if len(p.ElemType) > 0 {
		//	- must be integer, number, string or boolean
		if p.ElemType != "integer" && p.ElemType != "number" &&
			p.ElemType != "string" && p.ElemType != "boolean" {
			r = addError(r, fmt.Sprintf("%s elemType must be one of integer, number, string or boolean",
				pfx))
		}
	}
	return
}

//------------------------------------------------------------------------------
// stream

var rxPgChan = regexp.MustCompile(`^[A-Za-z\200-\377_][A-Za-z\200-\377_0-9\$]*$`)

func (s *Stream) validate(ds []Datasource) (r []ValidationResult) {
	// URI
	if !rxPrefix.MatchString(s.URI) && s.URI != "/" { // note: no {} allowed in components
		r = addError(r, fmt.Sprintf("stream %q: invalid URI", s.URI))
	}
	// Type
	if s.Type != "websocket" && s.Type != "sse" {
		r = addError(r, fmt.Sprintf("stream %q: invalid type %q", s.URI, s.Type))
	}
	// Channel
	if !rxPgChan.MatchString(s.Channel) {
		r = addError(r, fmt.Sprintf("stream %q: invalid channel %q", s.URI, s.Channel))
	}
	// Datasource
	found := false
	for i := range ds {
		if ds[i].Name == s.Datasource {
			found = true
			break
		}
	}
	if !found {
		r = addError(r, fmt.Sprintf("stream %q: unknown datasource %q", s.URI,
			s.Datasource))
	}
	return
}

//------------------------------------------------------------------------------
// datasource

var (
	rxName    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*(\.[A-Za-z0-9_][A-Za-z0-9_-]*)*$`)
	rxPqParam = regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	rxRole    = regexp.MustCompile(`^[A-Za-z\200-\377_][A-Za-z\200-\377_0-9\$]*$`)
)

func (d *Datasource) validate() (r []ValidationResult) {
	if !rxName.MatchString(d.Name) {
		r = addError(r, fmt.Sprintf("datasource %q: invalid name", d.Name))
	}
	if d.Params != nil {
		for k := range d.Params {
			if !rxPqParam.MatchString(k) {
				r = addError(r, fmt.Sprintf("datasource %q: invalid param %q",
					d.Name, k))
			}
		}
	}
	if d.Timeout != nil && *d.Timeout <= 0 {
		r = addWarn(r, fmt.Sprintf("datasource %q: timeout %g is <=0, will be ignored",
			d.Name, *d.Timeout))
	}
	if len(d.Role) > 0 && !rxRole.MatchString(d.Role) {
		r = addError(r, fmt.Sprintf("datasource %q: invalid role %q", d.Name,
			d.Role))
	}
	if len(d.SSLCert) > 0 && !fileExists(d.SSLCert) {
		r = addError(r, fmt.Sprintf("datasource %q: sslcert file %q does not exist",
			d.Name, d.SSLCert))
	}
	if len(d.SSLKey) > 0 && !fileExists(d.SSLKey) {
		r = addError(r, fmt.Sprintf("datasource %q: sslkey file %q does not exist",
			d.Name, d.SSLKey))
	}
	if len(d.SSLRootCert) > 0 && !fileExists(d.SSLRootCert) {
		r = addError(r, fmt.Sprintf("datasource %q: sslrootcert file %q does not exist",
			d.Name, d.SSLRootCert))
	}
	if d.Pool != nil {
		r = append(r, d.Pool.validate(d.Name)...)
	}
	return
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi != nil && fi.Mode().IsRegular()
}

//------------------------------------------------------------------------------
// datasource -> pool

func (p *ConnPool) validate(ds string) (r []ValidationResult) {
	if p.MinConns != nil && *p.MinConns <= 0 {
		r = addError(r, fmt.Sprintf("datasource %q: minConns for pool %d must be >0",
			ds, *p.MinConns))
	}
	if p.MaxConns != nil && *p.MaxConns <= 0 {
		r = addError(r, fmt.Sprintf("datasource %q: maxConns for pool %d must be >0",
			ds, *p.MaxConns))
	}
	if p.MaxConns != nil && p.MinConns != nil && *p.MaxConns < *p.MinConns {
		r = addError(r, fmt.Sprintf("datasource %q: maxConns for pool %d is < minConns %d",
			ds, *p.MaxConns, *p.MinConns))
	}
	if p.MaxIdleTime != nil && *p.MaxIdleTime <= 0 {
		r = addError(r, fmt.Sprintf("datasource %q: maxIdleTime for pool %g must be > 0",
			ds, *p.MaxIdleTime))
	}
	if p.MaxConnectedTime != nil && *p.MaxConnectedTime <= 0 {
		r = addError(r, fmt.Sprintf("datasource %q: maxConnected for pool %g must be > 0",
			ds, *p.MaxConnectedTime))
	}
	return
}

//------------------------------------------------------------------------------
// job

var stdCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func (j *Job) validate(ds []Datasource) (r []ValidationResult) {
	if !rxName.MatchString(j.Name) {
		r = addError(r, fmt.Sprintf("job %q: invalid name", j.Name))
	}
	if j.Type != "exec" && j.Type != "javascript" {
		r = addError(r,
			fmt.Sprintf("job %q: invalid type %q, must be one of 'exec' or 'javascript'",
				j.Name, j.Type))
	}
	if _, err := stdCronParser.Parse(j.Schedule); err != nil {
		r = addError(r,
			fmt.Sprintf("job %q: invalid cron schedule: %v", j.Name, err))
	}
	if j.Type == "exec" { // datasource required for exec
		found := false
		for i := range ds {
			if j.Datasource == ds[i].Name {
				found = true
				break
			}
		}
		if !found {
			r = addError(r,
				fmt.Sprintf("job %q: unknown datasource %q", j.Name, j.Datasource))
		}
	}
	if len(strings.TrimSpace(j.Script)) == 0 {
		r = addError(r,
			fmt.Sprintf("job %q: invalid script: empty", j.Name))
	}
	if j.TxOptions != nil {
		r = append(r, j.TxOptions.validate(fmt.Sprintf("job %q:", j.Name))...)
	}
	if j.Timeout != nil && *j.Timeout <= 0 {
		r = addWarn(r, fmt.Sprintf("job %q: timeout %g is <=0, will be ignored",
			j.Name, *j.Timeout))
	}
	return
}
