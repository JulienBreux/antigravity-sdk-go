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
	"sync"

	"github.com/JulienBreux/antigravity-sdk-go"
)

// HookContext provides a thread-safe hierarchical dictionary for hooks to share state.
type HookContext struct {
	parent *HookContext
	store  map[string]any
	mu     sync.RWMutex
}

func NewHookContext(parent *HookContext) *HookContext {
	return &HookContext{
		parent: parent,
		store:  make(map[string]any),
	}
}

func (c *HookContext) Get(key string, defaultValue any) any {
	c.mu.RLock()
	val, ok := c.store[key]
	c.mu.RUnlock()
	if ok {
		return val
	}
	if c.parent != nil {
		return c.parent.Get(key, defaultValue)
	}
	return defaultValue
}

func (c *HookContext) Set(key string, value any) {
	c.mu.Lock()
	c.store[key] = value
	c.mu.Unlock()
}

// Concrete Context Wrappers
type SessionContext struct{ *HookContext }
type TurnContext struct{ *HookContext }
type OperationContext struct{ *HookContext }

// Hook Interfaces

type OnSessionStartHook interface {
	Run(ctx *HookContext) error
}

type OnSessionStartFunc func(ctx *HookContext) error
func (f OnSessionStartFunc) Run(ctx *HookContext) error { return f(ctx) }


type OnSessionEndHook interface {
	Run(ctx *HookContext) error
}

type OnSessionEndFunc func(ctx *HookContext) error
func (f OnSessionEndFunc) Run(ctx *HookContext) error { return f(ctx) }


type PreTurnHook interface {
	Run(ctx *HookContext, prompt antigravity.Content) (antigravity.HookResult, error)
}

type PreTurnFunc func(ctx *HookContext, prompt antigravity.Content) (antigravity.HookResult, error)
func (f PreTurnFunc) Run(ctx *HookContext, prompt antigravity.Content) (antigravity.HookResult, error) {
	return f(ctx, prompt)
}


type PostTurnHook interface {
	Run(ctx *HookContext, response string) error
}

type PostTurnFunc func(ctx *HookContext, response string) error
func (f PostTurnFunc) Run(ctx *HookContext, response string) error { return f(ctx, response) }


type PreToolCallDecideHook interface {
	Run(ctx *HookContext, call antigravity.ToolCall) (antigravity.HookResult, error)
}

type PreToolCallDecideFunc func(ctx *HookContext, call antigravity.ToolCall) (antigravity.HookResult, error)
func (f PreToolCallDecideFunc) Run(ctx *HookContext, call antigravity.ToolCall) (antigravity.HookResult, error) {
	return f(ctx, call)
}


type PostToolCallHook interface {
	Run(ctx *HookContext, result antigravity.ToolResult) error
}

type PostToolCallFunc func(ctx *HookContext, result antigravity.ToolResult) error
func (f PostToolCallFunc) Run(ctx *HookContext, result antigravity.ToolResult) error { return f(ctx, result) }


type OnToolErrorHook interface {
	Run(ctx *HookContext, err error) (antigravity.HookResult, any, error)
}

type OnToolErrorFunc func(ctx *HookContext, err error) (antigravity.HookResult, any, error)
func (f OnToolErrorFunc) Run(ctx *HookContext, err error) (antigravity.HookResult, any, error) {
	return f(ctx, err)
}


type OnInteractionHook interface {
	Run(ctx *HookContext, spec antigravity.AskQuestionInteractionSpec) (antigravity.HookResult, *antigravity.QuestionHookResult, error)
}

type OnInteractionFunc func(ctx *HookContext, spec antigravity.AskQuestionInteractionSpec) (antigravity.HookResult, *antigravity.QuestionHookResult, error)
func (f OnInteractionFunc) Run(ctx *HookContext, spec antigravity.AskQuestionInteractionSpec) (antigravity.HookResult, *antigravity.QuestionHookResult, error) {
	return f(ctx, spec)
}


type OnCompactionHook interface {
	Run(ctx *HookContext, step antigravity.Step) error
}

type OnCompactionFunc func(ctx *HookContext, step antigravity.Step) error
func (f OnCompactionFunc) Run(ctx *HookContext, step antigravity.Step) error { return f(ctx, step) }
