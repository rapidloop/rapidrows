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
	"fmt"
	"strings"
)

// SchemaVersion is the semver version of the schema of the RapidRows API Server
// configuration file. Currently this is v1.0.0.
const SchemaVersion = "1.0.0"

//------------------------------------------------------------------------------
// core

// APIServerConfig is the entirety of the configuration supplied to the
// API Server. It is typically deserialized in from a .json or .yaml file.
type APIServerConfig struct {
	// Version indicates the version of the schema according to which the
	// other fields in this structure should be interpreted. This is in
	// the semver syntax (a trailing `.0` or `.0.0` may be omitted). This
	// field is required, and validation will fail without it.
	Version string `json:"version"`

	// Listen indicates the `IP` or `IP:port` for the server to bind to and
	// listen on. If the IP is omitted, the server will bind to all interfaces.
	// If port is omitted, it defaults to 8080. IP may be an IPv4 or IPv6
	// literal. Hostnames are not allowed. When specifying an IPv6 literal
	// along with a port, enclose the IPv6 literal within square brackets.
	// Examples: `127.0.0.1:8000`, `[02:42:04:e8:f7:33]:8080`, `:9000`,
	// `02:42:04:e8:f7:33`, `0.0.0.0:8080`
	Listen string `json:"listen,omitempty"`

	// CommonPrefix will be prefixed to each URI. If specified, must begin
	// with a slash, and must not end with one. Path components can contain
	// only A-Z, a-z, 0-9, _, . or -. Examples: `/api/v1`
	CommonPrefix string `json:"commonPrefix,omitempty"`

	// CORS specifies the Cross Object Resource Sharing configuration for the
	// server. This is optional, but note that CORS headers will not be added
	// if this is not configured (and therefore the APIs may not be callable
	// from browsers). See the documentation of the CORS struct for more info.
	CORS *CORS `json:"cors,omitempty"`

	// Compression enables the transparent use of gzip and deflate content
	// encoding. Outgoing responses from the server will be automatically
	// compressed using gzip or deflate if the client request indicates
	// support for it. Applies to the server as a whole, cannot be turned
	// on for individual URIs.
	Compression bool `json:"compression,omitempty"`

	// Endpoints is a list of all URIs implemented using queries or script.
	// See the documentation of Endpoint struct for more info. Optional.
	Endpoints []Endpoint `json:"endpoints,omitempty"`

	// Streams is a list of all websocket or Server Sent Event URIs. See
	// the documentation of Stream struct for more info. Optional.
	Streams []Stream `json:"streams,omitempty"`

	// Jobs is a list of all scheduled jobs. See the documentation of the
	// Job struct for more info. Optional.
	Jobs []Job `json:"jobs,omitempty"`

	// Datasources is a list of all PostgreSQL databases that can be referred
	// to by endpoints, streams and jobs. All datasources listed here will be
	// connected to on startup (unless it is explicitly marked as *lazy*).
	Datasources []Datasource `json:"datasources,omitempty"`
}

// Validate the entire configuration. Returns a list of errors and warnings.
func (c *APIServerConfig) Validate() (r []ValidationResult) {
	return c.validate()
}

// IsValid performs validation (calls Validate() internally) and returns an error
// if the validation finds at least one error. All errors are formatted into a
// single error message, and warnings are not included. For better formatting
// use the Validate() method directly.
func (c *APIServerConfig) IsValid() error {
	var a []string
	for _, r := range c.Validate() {
		if !r.Warn {
			a = append(a, r.Message)
		}
	}
	if len(a) > 0 {
		return fmt.Errorf("%d errors: %s", len(a), strings.Join(a, "; "))
	}
	return nil
}

// ValidationResult holds one entry of the results of validation. The Validate
// method of APIServerConfig returns a slice of these.
type ValidationResult struct {
	// Warn is true if the message is a warning, else it is an error.
	Warn bool

	// Message is the actual textual message describing the error or warning.
	Message string
}

//------------------------------------------------------------------------------
// endpoint

// Endpoint is a URI backed by an implementation that can:
//   - perform a SELECT-like SQL query and return the results in JSON or CSV
//   - execute a SQL query
//   - serve a static JSON or plain text data
//   - run the specified javascript code
type Endpoint struct {
	// URI denotes the path of the endpoint. The URI must start with a slash
	// but not end with one. Path components must consists of A-Z, a-z, 0-9,
	// _, . or -. If a path component is to serve as a parameter, it can be
	// wrapped in curly brackets. URI is case-sensitive.
	// Examples: `/user/{userid}`, `/repos/{owner}/{repo}/commits`
	// See also APIServerConfig.CommonPrefix.
	URI string `json:"uri"`

	// Methods configures the endpoint to accept HTTP requests only of the
	// specified methods. The value can be one of: `GET`, `POST`, `PUT`,
	// `PATCH` or `DELETE`. If omitted, the endpoint will respond to any
	// method.
	Methods []string `json:"methods,omitempty"`

	// Params is a list of parameters that will be accepted by this endpoint.
	// For SQL queries, the parameters will be made available as the bind
	// variables $1, $2 etc. For javascript, the parameters will be accessible
	// as an object $sys.params, with properties as parameter names. See
	// the documentation for Param struct for more info.
	Params []Param `json:"params,omitempty"`

	// ImplType is one of `query-json`, `query-csv`, `exec`, `static-text`,
	// `static-json` or `javascript`, and must be specified. For query-json,
	// query-csv and exec, the `Script` field should be a valid SQL statement.
	// For static-json the `Script` should be valid JSON. For javascript, the
	// `Script` should contain the javascript code.
	ImplType string `json:"implType"`

	// Datasource refers to one of the datasources listed in
	// APIServerConfig.Datasources. This field must be filled in for ImplType
	// of query-json, query-csv and exec. Ignored for other types.
	Datasource string `json:"datasource,omitempty"`

	// Script must be a valid SQL statement for query-json, query-csv or exec.
	// For static-text, it will hold plain text. For static-json this must be
	// valid JSON. For javascript, this should contain the javascript code.
	// For type exec and no params, multiple SQL statements are allowed.
	Script string `json:"script,omitempty"`

	// TxOptions allows running of query-json, query-csv and exec types within
	// a transaction. Ignored for other types. See the documentation of
	// TxOptions struct for more info.
	TxOptions *TxOptions `json:"tx,omitempty"`

	// Debug enables debug logging of all invocations of this endpoint.
	Debug bool `json:"debug,omitempty"`

	// Timeout in seconds for query-* and exec type. Ingored for other types.
	// Ignored if <= 0.
	Timeout *float64 `json:"timeout,omitempty"`

	// Cache the result for these many seconds. The APIServer should be started
	// with a RuntimeInterface that supports caching for this to work. The
	// cache entry is specific to the exact values of parameters for the
	// invocation. Ignored if <= 0.
	Cache *float64 `json:"cache,omitempty"`
}

// TxOptions specify what type of transaction to use for a SQL query. These
// correspond to the options used in the "BEGIN" or "SET TRANSACTION"
// statements in PostgreSQL.
type TxOptions struct {
	// Access is one of `read only` or `read write` (case insensitive). If
	// omitted, defaults to `read write`.
	Access string `json:"access,omitempty"`

	// ISOLevel is one of `serializable`, `repeatable read` or `read committed`
	// (case insensitive). If omitted, defaults to `read comitted`.
	ISOLevel string `json:"level,omitempty"`

	// Deferrable turns on the `deferrable` option for the transaction.
	Deferrable bool `json:"deferrable,omitempty"`
}

// Param represents a single parameter of an endpoint. A parameter can be
// passed in as part of the query parameters, as part of the URI path or
// in a json or form-encoded HTTP body. Some level of validation of the
// parameter is possible using the fields in this structure, for more checks
// use a javascript implementation. The server will return HTTP status code
// 400 if validation fails.
type Param struct {
	// Name is the name of the parameter, and is required. It has to be a
	// C-like identifier (first character A-Z, a-z; optionally followed by
	// A-Z, a-z, 0-9 or _.)
	Name string `json:"name"`

	// In specifies how the parameter will be passed, and is required. Must be
	// one of `query`, `path` or `body`. If `body` is specified, the parameter
	// maybe passed either as a form (application/x-www-form-urlencoded) or
	// a json object (application/json).
	In string `json:"in"`

	// Required indicates that the parameter, if not supplied, will be an
	// error (the server will return a HTTP status code 400). If it is not
	// required and not supplied, the SQL queries will receive a NULL as the
	// value of this parameter.
	Required bool `json:"required"`

	// Type of the parameter, required. Must be one of `integer`, `number`,
	// `string`, `boolean` or `array`. If the type is `number`, the value can
	// either be an integer or a float. If it is an `array`, the type of the
	// elements of the array have to be specified using the .ElemType field.
	Type string `json:"type"`

	// Enum can be used to specify a list of allowed values, only for types
	// string or integer or number. Note that if an enum is specified, other
	// types of validation (like mininum or maxLength) do not have any effect.
	Enum []any `json:"enum,omitempty"`

	// Minimum can be used to set the minimum allowed value for types integer
	// or number.
	Minimum *float64 `json:"minimum,omitempty"`

	// Maximum can be used to set the maximum allowed value for types integer
	// or number.
	Maximum *float64 `json:"maximum,omitempty"`

	// MaxLength can be used to set the maximum length for values of type
	// string.
	MaxLength *int `json:"maxLength,omitempty"`

	// Pattern is a regular expression that can be used for values of type
	// string. If set, the supplied parameter value should match this
	// regular expression. The regex syntax is RE2, most ES6 regexs should work.
	Pattern string `json:"pattern,omitempty"`

	// MinItems can be used to set the minimum number of elements for arrays.
	MinItems *int `json:"minItems,omitempty"`

	// MaxItems can be used to set the maximum number of elements for arrays.
	MaxItems *int `json:"maxItems,omitempty"`

	// ElemType specifies the type of individual elements if the Type field
	// is `array`. It is required for array type parameters. It must be one
	// of `integer`, `number`, `string` or `boolean`. Elements of varying types
	// and nested arrays are not allowed.
	ElemType string `json:"elemType,omitempty"`
}

//------------------------------------------------------------------------------
// stream

// Stream represents an endpoint that a WebSocket client or a Server-Sent-Events
// client can connect to, and receive notifications sent a PostgreSQL channel.
type Stream struct {
	// URI denotes the path of the endpoint. The URI must start with a slash
	// but not end with one. Path components must consists of A-Z, a-z, 0-9,
	// _, . or -. If a path component is to serve as a parameter, it can be
	// wrapped in curly brackets. URI is case-sensitive.
	// Examples: `/user/{userid}`, `/repos/{owner}/{repo}/commits`
	// See also APIServerConfig.CommonPrefix.
	URI string `json:"uri"`

	// Type is one of "websocket" or "sse", and is required.
	Type string `json:"type"`

	// Channel referes to the name of the PostgreSQL channel. Must be a valid
	// channel name, and must be specified.
	Channel string `json:"channel"`

	// Datasource is the name of the datasource listed in APIServerConfig.Datasources.
	// The channel specified above will refer to a channel in this database.
	Datasource string `json:"datasource"`

	// Debug enables debug logging of all invocations of this endpoint.
	Debug bool `json:"debug,omitempty"`
}

//------------------------------------------------------------------------------
// cors

// CORS specifies the Cross Origin Resource Sharing configuration for the
// server.
type CORS struct {
	// AllowedOrigins is a list of origins a cross-domain request can be executed from.
	// If the special `*` value is present in the list, all origins will be allowed.
	// An origin may contain a wildcard (*) to replace 0 or more characters
	// (i.e.: http://*.domain.com). Only one wildcard can be used per origin.
	// Default value is [`*`].
	AllowedOrigins []string `json:"allowedOrigins,omitempty"`

	// AllowedMethods is a list of methods the client is allowed to use with
	// cross-domain requests. Default value is [`HEAD`, `GET`, `POST`].
	AllowedMethods []string `json:"allowedMethods,omitempty"`

	// AllowedHeaders is list of non simple headers the client is allowed to use
	// with  cross-domain requests. If the special `*` value is present in the
	// list, all headers will be allowed. Default value is [] but `Origin` is
	// always appended to the list.
	AllowedHeaders []string `json:"allowedHeaders,omitempty"`

	// ExposedHeaders indicates which headers are safe to expose to the API of
	// a CORS API specification.
	ExposedHeaders []string `json:"exposedHeaders,omitempty"`

	// AllowCredentials indicates whether the request can include user
	// credentials like cookies, HTTP authentication or client side SSL
	// certificates.
	AllowCredentials bool `json:"allowCredentials,omitempty"`

	// MaxAge indicates how long (in seconds) the results of a preflight request
	// can be cached without sending another preflight request.
	MaxAge *int `json:"maxAge,omitempty"`

	// Debug enables logging of CORS-related decisions for every endpoint.
	Debug bool `json:"debug,omitempty"`
}

//------------------------------------------------------------------------------
// datasource

// Datasource defines the parameters to connect to a data source. Currently a
// data source is a PostgreSQL database, and each instance of a Datasource
// struct contains the equivalent of a connection URI or DSN. The following
// environment variables are understood: PGHOST, PGPORT, PGDATABASE, PGUSER,
// PGPASSWORD, PGPASSFILE, PGSERVICE, PGSERVICEFILE, PGSSLMODE, PGSSLCERT,
// PGSSLKEY, PGSSLROOTCERT, PGSSLPASSWORD, PGAPPNAME, PGCONNECT_TIMEOUT and
// PGTARGETSESSIONATTRS (see https://www.postgresql.org/docs/current/libpq-envars.html
// for usage).
type Datasource struct {
	// Name uniquely identifies a datasource, and must be specified. It is
	// of the format of a fully qualified domain name.
	// Examples: `prod-us-east-1`, `pgsrv03.acme.com`
	Name string `json:"name"`

	// Host is an IP, a hostname or a Unix socket path to the listening
	// Postgres server. Can include `:port` suffix to override the default
	// port of 5432. Can include multiple comma-separated hosts.
	Host string `json:"host,omitempty"`

	// Database is the name of the Postgres database to connect to. If
	// omitted, will default to the name of the system user the server is
	// running as.
	Database string `json:"dbname,omitempty"`

	// User is the PostgreSQL user name to connect as. Defaults to be the same
	// as the operating system name of the user running the application.
	User string `json:"user,omitempty"`

	// Password to be used if the server demands password authentication.
	// This is in plain text, and is preferrable to use a Passfile instead.
	Password string `json:"password,omitempty"`

	// Passfile pecifies the name of the file used to store passwords.
	// See https://www.postgresql.org/docs/current/libpq-pgpass.html.
	Passfile string `json:"passfile,omitempty"`

	// SSLMode is one of `disable`, `allow`, `prefer`, `require`, `verify-ca`
	// or `verify-full`. See [PostgreSQL docs](https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNECT-SSLMODE)
	// for more details.
	SSLMode string `json:"sslmode,omitempty"`

	// SSLCert specifies the file name of the client SSL certificate,
	// replacing the default `~/.postgresql/postgresql.crt`. This parameter is
	// ignored if an SSL connection is not made.
	SSLCert string `json:"sslcert,omitempty"`

	// SSLKey specifies the location for the secret key used for the client
	// certificate, to be used instead of `~/.postgresql/postgresql.key`.
	SSLKey string `json:"sslkey,omitempty"`

	// SSLRootCert specifies the name of a file containing SSL certificate
	// authority (CA) certificate(s). If the file exists, the server's
	// certificate will be verified to be signed by one of these authorities.
	// The default is `~/.postgresql/root.crt`.
	SSLRootCert string `json:"sslrootcert,omitempty"`

	// Params specified additional connection parameters, like
	// `application_name` or `search_path`.
	Params map[string]string `json:"params,omitempty"`

	// PreferSimpleProtocol disables implicit prepared statement usage. Set
	// this to true if you are connecting to a connection pooler that requires
	// the use of PostgreSQL simple protocol.
	PreferSimpleProtocol bool `json:"simple,omitempty"`

	// Timeout specifies a timeout for establishing the connection, in seconds.
	// Ignored if <= 0.
	Timeout *float64 `json:"timeout,omitempty"`

	// Role specifies a PostgreSQL role that will be set immediately upon
	// connection. If set, must be a valid PostgreSQL role in the database.
	Role string `json:"role,omitempty"`

	// Pool configures the connection pooling parameters for this datasource.
	// If no pool is configured for this datasource, connections to the
	// PostgreSQL server are made as and when necessary without restraint.
	Pool *ConnPool `json:"pool,omitempty"`
}

// ConnPool specifies the settings for pooling of connections for a single
// datasource. All settings in this struct are optional.
type ConnPool struct {
	// MinConns sets the minimum number of connections in the pool. If
	// specified, must be > 0.
	MinConns *int64 `json:"minConns,omitempty"`

	// MaxConns sets the maximum number of connections to the database that
	// will be established. Defaults to max(4, number-of-CPUs). If specified,
	// must be > 0.
	MaxConns *int64 `json:"maxConns,omitempty"`

	// MaxIdleTime in seconds is the duration after which an idle connection
	// will be automatically closed. If specified, must be > 0.
	MaxIdleTime *float64 `json:"maxIdleTime,omitempty"`

	// MaxConnectedTime in seconds is the duration since creation after which
	// a connection will be automatically closed.  If specified, must be > 0.
	MaxConnectedTime *float64 `json:"maxConnectedTime,omitempty"`

	// Lazy if set means that the connections will be established only on
	// first demand and not at server startup.
	Lazy bool `json:"lazy,omitempty"`
}

//------------------------------------------------------------------------------
// scheduled jobs

// Job represents a single scheduled job that will be run at the specified
// interval (a CRON schedule). The job itself can be SQL statement(s) that
// are executed on a specified datasource, or javascript code.
type Job struct {
	// Name uniquely identifies a job, and must be specified. It is
	// of the format of a fully qualified domain name.
	// Examples: `mkparts.daily`, `proj3-weekly-reports`
	Name string `json:"name"`

	// Type is one of `exec` or `javascript`, and must be specified. If the
	// type is `exec`, then a Datasource and Script must also be specified. In
	// case of `javascript`, the Script field must contain the javascript code.
	Type string `json:"type"`

	// Schedule is the CRON-style 5-part schedule for the job. Additionally,
	// strings like `@every 5m` are also accepted. See the main documentation
	// for more details.
	// Examples: `0 12 * * 1`, `23 0-20/2 * * *`, `@every 30m`
	Schedule string `json:"schedule"`

	// Datasource refers to one of the datasources listed in
	// APIServerConfig.Datasources. This field must be filled in for type `exec`.
	Datasource string `json:"datasource,omitempty"`

	// Script is either the SQL statements (in case of `exec`) or the javascript
	// code (in case of `javascript`). Either way, it must be specified and
	// cannot be empty.
	Script string `json:"script"`

	// TxOptions allows running of SQL statements (when type is `exec`) within
	// a transaction. See the documentation of TxOptions struct for more info.
	TxOptions *TxOptions `json:"tx,omitempty"`

	// Debug enables debug logging of all invocations of this job.
	Debug bool `json:"debug,omitempty"`

	// Timeout if set, specifies a timeout in seconds for the running of
	// SQL statements specified for `exec` type jobs. Ignored for other types.
	// Ignored if <= 0.
	Timeout *float64 `json:"timeout,omitempty"`
}

//------------------------------------------------------------------------------
// results

type queryResult struct {
	Rows  [][]any `json:"rows"`
	Error string  `json:"error,omitempty"`
}

type execResult struct {
	RowsAffected int64  `json:"rowsAffected"`
	Error        string `json:"error,omitempty"`
}
