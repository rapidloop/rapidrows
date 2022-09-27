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

const cfgTestJobsBasic = `{
	"version": "1",
	"listen": "127.0.0.1:60000",
	"endpoints": [
		{
			"uri": "/setup",
			"implType": "exec",
			"script": "drop table if exists movies; create table movies (name text, year integer); insert into movies values ('The Shawshank Redemption', 1994), ('The Godfather', 1972), ('The Dark Knight', 2008), ('The Godfather Part II', 1974), ('12 Angry Men', 1957);",
			"datasource": "default"
		}
	],
	"jobs": [
		{
			"name": "job1",
			"type": "exec",
			"schedule": "@every 1s",
			"datasource": "default",
			"script": "update movies set name=name where year=1972",
			"timeout": 3,
			"debug": true
		},
		{
			"name": "job2",
			"type": "exec",
			"schedule": "@every 1s",
			"datasource": "default",
			"script": "** syntax error",
			"debug": true
		},
		{
			"name": "job3",
			"type": "javascript",
			"schedule": "@every 1s",
			"script": "throw 'foo'"
		}
	],
	"datasources": [ {"name": "default"} ]
}`

func TestJobsBasic(t *testing.T) {
	r := require.New(t)

	cfg := loadCfg(r, cfgTestJobsBasic)
	s := startServerFull(r, cfg)

	time.Sleep(3 * time.Second)

	s.Stop(time.Second)
}
