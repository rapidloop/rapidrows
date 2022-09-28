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
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/rapidloop/rapidrows"
	"github.com/stretchr/testify/require"
)

const (
	invalidCfgs = "_test/invalid_cfgs.jsons"
	warnCfgs    = "_test/warn_cfgs.jsons"
)

func TestValidateConfigError(t *testing.T) {
	r := require.New(t)

	f, err := os.Open(invalidCfgs)
	r.Nil(err)
	defer f.Close()

	dec := json.NewDecoder(f)
	for {
		var cfg rapidrows.APIServerConfig
		if err := dec.Decode(&cfg); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("bad json in %s: %v", invalidCfgs, err)
		}
		if err := cfg.IsValid(); err == nil {
			t.Fatalf("invalid config passes:\n%+v\n", cfg)
		} else {
			t.Logf("error (expected): %v", err)
		}
	}
}

func TestValidateConfigWarn(t *testing.T) {
	r := require.New(t)

	f, err := os.Open(warnCfgs)
	r.Nil(err)
	defer f.Close()

	dec := json.NewDecoder(f)
	for {
		var cfg rapidrows.APIServerConfig
		if err := dec.Decode(&cfg); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("bad json in %s: %v", warnCfgs, err)
		}
		count := 0
		for _, vr := range cfg.Validate() {
			r.True(vr.Warn, vr.Message)
			r.Greater(len(vr.Message), 0)
			t.Logf("warning (expected): %s", vr.Message)
			count++
		}
		r.Greater(count, 0, "at least 1 warning was expected")
	}
}
