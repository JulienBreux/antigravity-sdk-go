// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package triggers

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/JulienBreux/antigravity-sdk-go"
)

// TriggerRunner manages registration, startup, and shutdown of triggers.
type TriggerRunner struct {
	triggers  []Trigger
	conn      antigravity.Connection
	mu        sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	isRunning bool
}

func init() {
	antigravity.DefaultNewTriggerRunner = func(trigs []any, conn any) antigravity.TriggerRunner {
		var concreteTrigs []Trigger
		for _, tg := range trigs {
			if t, ok := tg.(Trigger); ok {
				concreteTrigs = append(concreteTrigs, t)
			} else if t2, ok := tg.(func(context.Context, *TriggerContext) error); ok {
				concreteTrigs = append(concreteTrigs, Trigger(t2))
			}
		}
		return NewTriggerRunner(concreteTrigs, conn.(antigravity.Connection))
	}
}

// NewTriggerRunner initializes the TriggerRunner.
func NewTriggerRunner(trgs []Trigger, conn antigravity.Connection) *TriggerRunner {
	return &TriggerRunner{
		triggers: trgs,
		conn:     conn,
	}
}

// Start starts all triggers as concurrent goroutines.
// Each trigger receives its own TriggerContext. If a trigger returns
// an error, it is logged and the task stops.
func (r *TriggerRunner) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isRunning {
		return errors.New("TriggerRunner is already started")
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.isRunning = true

	for i, trg := range r.triggers {
		r.wg.Add(1)
		go func(t Trigger, idx int) {
			defer r.wg.Done()
			tc := NewTriggerContext(r.conn)
			err := t(runCtx, tc)
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("Trigger %d failed with unhandled error: %v", idx, err)
			}
		}(trg, i)
	}

	return nil
}

// Stop cancels all trigger tasks and waits for them to finish.
func (r *TriggerRunner) Stop() {
	r.mu.Lock()
	if !r.isRunning {
		r.mu.Unlock()
		return
	}
	cancel := r.cancel
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	r.wg.Wait()

	r.mu.Lock()
	r.isRunning = false
	r.cancel = nil
	r.mu.Unlock()
}

// IsRunning returns true if the runner is currently running.
func (r *TriggerRunner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isRunning
}
