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
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
)

//------------------------------------------------------------------------------
// cron

func newCron(logger zerolog.Logger) *cron.Cron {
	l := loggerForCron{logger}
	return cron.New(cron.WithLogger(&l))
}

type loggerForCron struct {
	logger zerolog.Logger
}

func (l *loggerForCron) Info(msg string, keysAndValues ...interface{}) {
	// too verbose
	/*
		e := l.logger.Info().Bool("crond", true)
		for i := 0; i < len(keysAndValues)/2; i += 2 {
			e = e.Str(fmt.Sprintf("%v", keysAndValues[i]), fmt.Sprintf("%v", keysAndValues[i+1]))
		}
		e.Msg(msg)
	*/
}

func (l *loggerForCron) Error(err error, msg string, keysAndValues ...interface{}) {
	e := l.logger.Error().Err(err).Bool("crond", true)
	for i := 0; i < len(keysAndValues)/2; i += 2 {
		e = e.Str(fmt.Sprintf("%v", keysAndValues[i]), fmt.Sprintf("%v", keysAndValues[i+1]))
	}
	e.Msg(msg)
}

//------------------------------------------------------------------------------
// jobs

func (a *APIServer) setupJobs() error {
	// schedule all jobs
	for i, job := range a.cfg.Jobs {
		if _, err := a.c.AddFunc(job.Schedule, a.jobRunner(i)); err != nil {
			a.logger.Error().Err(err).Str("job", job.Name).Msg("failed to schedule job")
			return fmt.Errorf("failed to schedule job %q: %v", job.Name, err)
		}
	}

	return nil
}

func (a *APIServer) jobRunner(idx int) func() {
	return func() {
		a.runJob(&a.cfg.Jobs[idx])
	}
}

func (a *APIServer) runJob(job *Job) {
	t0 := time.Now()
	logger := a.logger.With().Str("job", job.Name).Logger()
	if job.Debug {
		logger.Debug().Msg("job starting")
	}

	if job.Type == "exec" {
		// make context
		ctx := a.bgctx
		if job.Timeout != nil && *job.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(*job.Timeout*float64(time.Second)))
			defer cancel()
		}

		// run query
		cb := func(q querier) error {
			_, err := q.Exec(ctx, job.Script)
			return err
		}
		if err := a.ds.withTx(job.Datasource, job.TxOptions, cb); err != nil {
			logger.Error().Err(err).Msg("exec failed")
			return
		}
	} else if job.Type == "javascript" {
		if _, _, err := a.runScript(job.Script, make(map[string]any), logger, job.Debug); err != nil {
			logger.Error().Err(err).Msg("javascript execution failed")
		}
	}

	if job.Debug {
		logger.Debug().Float64("elapsed", float64(time.Since(t0))/1e6).
			Msg("job completed successfully")
	}
}
