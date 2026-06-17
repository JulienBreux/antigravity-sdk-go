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

package tools

import (
	"context"
	"errors"
	"sync"

	"github.com/JulienBreux/antigravity-sdk-go"
)

// ToolRunner registry and executor for custom tools.
type ToolRunner struct {
	tools map[string]Tool
	ctx   *ToolContext
	mu    sync.RWMutex
}

func init() {
	antigravity.DefaultNewToolRunner = func(t []any) antigravity.ToolRunner {
		var concreteTools []Tool
		for _, item := range t {
			if tool, ok := item.(Tool); ok {
				concreteTools = append(concreteTools, tool)
			}
		}
		return NewToolRunner(concreteTools)
	}
}

func NewToolRunner(tools []Tool) *ToolRunner {
	tr := &ToolRunner{
		tools: make(map[string]Tool),
	}
	for _, t := range tools {
		tr.Register(t)
	}
	return tr
}

func (tr *ToolRunner) SetContext(conn any) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.ctx = NewToolContext(conn.(antigravity.Connection))
}

func (tr *ToolRunner) Register(tool Tool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.tools[tool.Name] = tool
}

func (tr *ToolRunner) Unregister(name string) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if _, ok := tr.tools[name]; !ok {
		return errors.New("tool not registered")
	}
	delete(tr.tools, name)
	return nil
}

func (tr *ToolRunner) Tools() map[string]Tool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make(map[string]Tool)
	for k, v := range tr.tools {
		out[k] = v
	}
	return out
}

func (tr *ToolRunner) Execute(ctx context.Context, name string, args map[string]any) (any, error) {
	tr.mu.RLock()
	tool, ok := tr.tools[name]
	toolCtx := tr.ctx
	tr.mu.RUnlock()

	if !ok {
		return nil, errors.New("tool not found")
	}

	return tool.callback(ctx, toolCtx, args)
}

func (tr *ToolRunner) ProcessToolCalls(ctx context.Context, calls []antigravity.ToolCall) []antigravity.ToolResult {
	results := make([]antigravity.ToolResult, len(calls))
	var wg sync.WaitGroup

	for i, tc := range calls {
		wg.Add(1)
		go func(idx int, call antigravity.ToolCall) {
			defer wg.Done()
			res, err := tr.Execute(ctx, call.Name, call.Args)
			result := antigravity.ToolResult{
				Name: call.Name,
				ID:   call.ID,
			}
			if err != nil {
				errStr := err.Error()
				result.Error = &errStr
				result.Exception = err
			} else {
				result.Result = res
			}
			results[idx] = result
		}(i, tc)
	}
	wg.Wait()
	return results
}
