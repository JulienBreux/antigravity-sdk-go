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

package hooks

import (
	"fmt"
	"sync"

	"github.com/JulienBreux/antigravity-sdk-go"
)

// HookRunner manages collections of specific hook types and dispatches events.
type HookRunner struct {
	onSessionStartHooks    []OnSessionStartHook
	onSessionEndHooks      []OnSessionEndHook
	preTurnHooks           []PreTurnHook
	postTurnHooks          []PostTurnHook
	preToolCallDecideHooks []PreToolCallDecideHook
	postToolCallHooks      []PostToolCallHook
	onToolErrorHooks       []OnToolErrorHook
	onInteractionHooks     []OnInteractionHook
	onCompactionHooks      []OnCompactionHook

	SessionContext SessionContext
	mu             sync.RWMutex
}

func init() {
	antigravity.DefaultNewHookRunner = func() antigravity.HookRunner {
		return NewHookRunner()
	}
}

func NewHookRunner() *HookRunner {
	return &HookRunner{
		SessionContext: SessionContext{HookContext: NewHookContext(nil)},
	}
}

func (hr *HookRunner) RegisterHook(hook any) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	matched := false
	if h, ok := hook.(OnSessionStartHook); ok {
		hr.onSessionStartHooks = append(hr.onSessionStartHooks, h)
		matched = true
	}
	if h, ok := hook.(OnSessionEndHook); ok {
		hr.onSessionEndHooks = append(hr.onSessionEndHooks, h)
		matched = true
	}
	if h, ok := hook.(PreTurnHook); ok {
		hr.preTurnHooks = append(hr.preTurnHooks, h)
		matched = true
	}
	if h, ok := hook.(PostTurnHook); ok {
		hr.postTurnHooks = append(hr.postTurnHooks, h)
		matched = true
	}
	if h, ok := hook.(PreToolCallDecideHook); ok {
		hr.preToolCallDecideHooks = append(hr.preToolCallDecideHooks, h)
		matched = true
	}
	if h, ok := hook.(PostToolCallHook); ok {
		hr.postToolCallHooks = append(hr.postToolCallHooks, h)
		matched = true
	}
	if h, ok := hook.(OnToolErrorHook); ok {
		hr.onToolErrorHooks = append(hr.onToolErrorHooks, h)
		matched = true
	}
	if h, ok := hook.(OnInteractionHook); ok {
		hr.onInteractionHooks = append(hr.onInteractionHooks, h)
		matched = true
	}
	if h, ok := hook.(OnCompactionHook); ok {
		hr.onCompactionHooks = append(hr.onCompactionHooks, h)
		matched = true
	}

	if !matched {
		panic(fmt.Sprintf("unknown hook type registered: %T", hook))
	}
}

// Session Dispatches
func (hr *HookRunner) DispatchSessionStart() error {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	for _, h := range hr.onSessionStartHooks {
		if err := h.Run(hr.SessionContext.HookContext); err != nil {
			return err
		}
	}
	return nil
}

func (hr *HookRunner) DispatchSessionEnd() error {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	for _, h := range hr.onSessionEndHooks {
		if err := h.Run(hr.SessionContext.HookContext); err != nil {
			return err
		}
	}
	return nil
}

// Turn Dispatches
func (hr *HookRunner) DispatchPreTurn(prompt antigravity.Content) (antigravity.HookResult, TurnContext, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	ctx := TurnContext{HookContext: NewHookContext(hr.SessionContext.HookContext)}
	for _, h := range hr.preTurnHooks {
		res, err := h.Run(ctx.HookContext, prompt)
		if err != nil {
			return res, ctx, err
		}
		if !res.Allow {
			return res, ctx, nil
		}
	}
	return antigravity.HookResult{Allow: true}, ctx, nil
}

func (hr *HookRunner) DispatchPostTurn(ctx TurnContext, response string) error {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	for _, h := range hr.postTurnHooks {
		if err := h.Run(ctx.HookContext, response); err != nil {
			return err
		}
	}
	return nil
}

// Tool Dispatches
func (hr *HookRunner) DispatchPreToolCall(ctx TurnContext, call antigravity.ToolCall) (antigravity.HookResult, antigravity.ToolCall, OperationContext, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	opCtx := OperationContext{HookContext: NewHookContext(ctx.HookContext)}
	for _, h := range hr.preToolCallDecideHooks {
		res, err := h.Run(opCtx.HookContext, call)
		if err != nil {
			return res, call, opCtx, err
		}
		if !res.Allow {
			return res, call, opCtx, nil
		}
	}
	return antigravity.HookResult{Allow: true}, call, opCtx, nil
}

func (hr *HookRunner) DispatchPostToolCall(opCtx OperationContext, result antigravity.ToolResult) error {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	for _, h := range hr.postToolCallHooks {
		if err := h.Run(opCtx.HookContext, result); err != nil {
			return err
		}
	}
	return nil
}

func (hr *HookRunner) DispatchOnToolError(opCtx OperationContext, toolErr error) (antigravity.HookResult, any, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	for _, h := range hr.onToolErrorHooks {
		res, val, err := h.Run(opCtx.HookContext, toolErr)
		if err != nil {
			return res, nil, err
		}
		if res.Allow {
			return res, val, nil
		}
	}
	return antigravity.HookResult{Allow: false}, nil, nil
}

// Interaction Dispatch
func (hr *HookRunner) DispatchInteraction(ctx TurnContext, spec antigravity.AskQuestionInteractionSpec) (antigravity.HookResult, *antigravity.QuestionHookResult, OperationContext, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	opCtx := OperationContext{HookContext: NewHookContext(ctx.HookContext)}
	for _, h := range hr.onInteractionHooks {
		res, val, err := h.Run(opCtx.HookContext, spec)
		if err != nil {
			return res, nil, opCtx, err
		}
		if res.Allow {
			return res, val, opCtx, nil
		}
	}
	return antigravity.HookResult{Allow: false, Message: "No interaction hook handled the request"}, nil, opCtx, nil
}

// Compaction Dispatch
func (hr *HookRunner) DispatchCompaction(ctx TurnContext, step antigravity.Step) error {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	opCtx := OperationContext{HookContext: NewHookContext(ctx.HookContext)}
	for _, h := range hr.onCompactionHooks {
		if err := h.Run(opCtx.HookContext, step); err != nil {
			return err
		}
	}
	return nil
}

// HasPreToolCallDecideHook returns true if any PreToolCallDecideHooks are registered.
func (hr *HookRunner) HasPreToolCallDecideHook() bool {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return len(hr.preToolCallDecideHooks) > 0
}
