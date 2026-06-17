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
	"time"

	"github.com/JulienBreux/antigravity-sdk-go"
	"github.com/fsnotify/fsnotify"
)

// TriggerContext wraps a connection and provides a safe interface for triggers to send messages.
type TriggerContext struct {
	conn antigravity.Connection
}

func NewTriggerContext(conn antigravity.Connection) *TriggerContext {
	return &TriggerContext{conn: conn}
}

func (c *TriggerContext) Send(ctx context.Context, content string) error {
	return c.conn.SendTriggerNotification(ctx, content)
}

// Trigger is a long-lived task that runs in the background.
type Trigger func(ctx context.Context, tc *TriggerContext) error

// Every creates a trigger that executes a callback periodically.
func Every(interval time.Duration, callback func(ctx context.Context, tc *TriggerContext) error) Trigger {
	return func(ctx context.Context, tc *TriggerContext) error {
		if interval <= 0 {
			return errors.New("interval must be positive")
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if err := callback(ctx, tc); err != nil {
					return err
				}
			}
		}
	}
}

// OnFileChange creates a trigger that monitors file/folder updates.
func OnFileChange(path string, callback func(ctx context.Context, tc *TriggerContext, changes []antigravity.FileChange) error) Trigger {
	return func(ctx context.Context, tc *TriggerContext) error {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		defer watcher.Close()

		if err := watcher.Add(path); err != nil {
			return err
		}

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case event, ok := <-watcher.Events:
				if !ok {
					return nil
				}
				var kind antigravity.FileChangeKind
				if event.Has(fsnotify.Create) {
					kind = antigravity.FileChangeAdded
				} else if event.Has(fsnotify.Write) {
					kind = antigravity.FileChangeModified
				} else if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					kind = antigravity.FileChangeDeleted
				} else {
					kind = antigravity.FileChangeModified
				}
				change := antigravity.FileChange{
					Kind: kind,
					Path: event.Name,
				}
				if err := callback(ctx, tc, []antigravity.FileChange{change}); err != nil {
					return err
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return nil
				}
				log.Printf("file watcher error: %v", err)
			}
		}
	}
}
