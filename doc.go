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

// The package rapidrows provides the definition of the API server configuration
// (the [APIServerConfig] structure and it's children), as well as the
// implementation of the API server itself ([APIServer]). Runtime dependencies
// to be supplied by the caller are specified using the [RuntimeInterface].
//
// Refer to the main RapidRows documentation for more in-depth explanation
// of features as well as examples. The code for the `cmd/rapidrows` CLI tool
// is a good example of how to use the APIServer.
package rapidrows
