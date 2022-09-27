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

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/mattn/go-isatty"
	"github.com/rapidloop/rapidrows"
	"github.com/rs/zerolog"
	"github.com/spf13/pflag"
)

var (
	flagset  = pflag.NewFlagSet("", pflag.ContinueOnError)
	fversion = flagset.BoolP("version", "v", false, "show version and exit")
	fcheck   = flagset.BoolP("check", "c", false, "only check if the config file is valid")
	flog     = flagset.StringP("logtype", "l", "text", "print logs in 'text' (default) or 'json' format")
	fnocolor = flagset.Bool("no-color", false, "do not colorize log output")
	fyaml    = flagset.BoolP("yaml", "y", false, "config-file is in YAML format")
)

var version string // set during build

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: rapidrows [options] config-file
RapidRows is a single-binary configurable API server.

Options:
`)
	flagset.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
(c) RapidLoop, Inc. 2022 * https://rapidrows.io
`)
}

func main() {
	flagset.Usage = usage
	if err := flagset.Parse(os.Args[1:]); err == pflag.ErrHelp {
		return
	} else if err != nil || (!*fversion && flagset.NArg() != 1) || (*flog != "text" && *flog != "json") {
		usage()
		os.Exit(1)
	}

	log.SetFlags(0)
	if *fversion {
		fmt.Printf("rapidrows v%s\n(c) RapidLoop, Inc. 2022 * https://rapidrows.io\n",
			version)
		return
	}
	os.Exit(realmain())
}

func realmain() int {
	// read input file & validate
	raw, err := os.ReadFile(flagset.Arg(0))
	if err != nil {
		log.Printf("rapidrows: failed to read input: %v", err)
		return 1
	}
	var config rapidrows.APIServerConfig
	if *fyaml {
		if err := yaml.Unmarshal(raw, &config); err != nil {
			log.Printf("rapidrows: failed to decode yaml: %v", err)
			return 1
		}
	} else {
		if err := json.Unmarshal(raw, &config); err != nil {
			log.Printf("rapidrows: failed to decode json: %v", err)
			return 1
		}
	}

	if *fcheck { // if only check was requested, check, print and exit
		var w, e int
		for _, r := range config.Validate() {
			if r.Warn {
				fmt.Print("warning: ")
				w++
			} else {
				fmt.Print("error: ")
				e++
			}
			fmt.Println(r.Message)
		}
		if w > 0 || e > 0 {
			fmt.Printf("\n%s: %d error(s), %d warning(s)\n", flagset.Arg(0), e, w)
		}
		if e > 0 {
			return 2
		}
		return 0
	}

	// start the server
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	var logger zerolog.Logger
	if *flog == "json" {
		logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	} else {
		out := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "2006-01-02 15:04:05.999",
			NoColor:    !isatty.IsTerminal(os.Stdout.Fd()) || *fnocolor,
		}
		logger = zerolog.New(out).With().Timestamp().Logger()
	}
	rti := rapidrows.RuntimeInterface{
		Logger:   &logger,
		CacheSet: cacheSet,
		CacheGet: cacheGet,
	}
	server, err := rapidrows.NewAPIServer(&config, &rti)
	if err != nil {
		log.Printf("rapidrows: failed to create server: %v", err)
		return 1
	}
	if err := server.Start(); err != nil {
		log.Printf("rapidrows: failed to start server: %v", err)
		return 1
	}

	// wait for ^C
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
	signal.Stop(ch)
	close(ch)

	// stop the server
	if err := server.Stop(time.Minute); err != nil {
		log.Printf("rapidrows: warning: failed to stop server: %v", err)
	}

	return 0
}

var cache sync.Map

func cacheSet(key uint64, value []byte) {
	if len(value) == 0 {
		cache.Delete(key)
	} else {
		cache.Store(key, value)
	}
}

func cacheGet(key uint64) (value []byte, found bool) {
	if v, ok := cache.Load(key); ok && v != nil {
		return v.([]byte), true
	}
	return nil, false
}
