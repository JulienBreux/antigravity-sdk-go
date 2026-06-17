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

package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/JulienBreux/antigravity-sdk-go"
	_ "github.com/JulienBreux/antigravity-sdk-go/conversation"
	"github.com/JulienBreux/antigravity-sdk-go/hooks"
	"github.com/JulienBreux/antigravity-sdk-go/tools"
	"github.com/JulienBreux/antigravity-sdk-go/triggers"
)

// Agent provides the high-level Agent API for simplified interaction.
type Agent struct {
	config        antigravity.AgentConfig
	strategy      antigravity.ConnectionStrategy
	conversation  antigravity.Conversation
	toolRunner    *tools.ToolRunner
	hookRunner    *hooks.HookRunner
	triggerRunner *triggers.TriggerRunner
	pendingHooks  []any
	pendingTrigs  []triggers.Trigger
	mu            sync.Mutex
	isStarted     bool
}

// NewAgent initializes the Agent with declarative configuration.
func NewAgent(config antigravity.AgentConfig) *Agent {
	baseCfg := config.GetBaseConfig()
	agent := &Agent{
		config: config,
	}

	if baseCfg.Hooks != nil {
		agent.pendingHooks = append(agent.pendingHooks, baseCfg.Hooks...)
	}

	if baseCfg.Triggers != nil {
		for _, tg := range baseCfg.Triggers {
			if trig, ok := tg.(triggers.Trigger); ok {
				agent.pendingTrigs = append(agent.pendingTrigs, trig)
			}
		}
	}

	return agent
}

// RegisterHook registers a hook by inferring its type.
func (a *Agent) RegisterHook(hook any) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.hookRunner == nil {
		a.pendingHooks = append(a.pendingHooks, hook)
		return
	}
	a.hookRunner.RegisterHook(hook)
}

// RegisterTrigger registers a background trigger.
// Returns an error if the agent is already started.
func (a *Agent) RegisterTrigger(trigger triggers.Trigger) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.isStarted {
		return errors.New("cannot register triggers after the agent has started")
	}
	a.pendingTrigs = append(a.pendingTrigs, trigger)
	return nil
}

// Start starts the agent session, initializing connections, safety policies, tools, and triggers.
func (a *Agent) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.isStarted {
		return errors.New("agent session already started")
	}

	a.hookRunner = hooks.NewHookRunner()
	for _, hook := range a.pendingHooks {
		a.hookRunner.RegisterHook(hook)
	}
	a.pendingHooks = nil

	baseCfg := a.config.GetBaseConfig()

	// Apply response schema override to finish tool capability
	if baseCfg.ResponseSchema != nil {
		baseCfg.Capabilities.FinishToolSchemaJSON = baseCfg.ResponseSchema
	}

	// Apply policies
	var activePolicies []hooks.Policy
	for _, p := range baseCfg.Policies {
		if policy, ok := p.(hooks.Policy); ok {
			activePolicies = append(activePolicies, policy)
		} else if policyList, ok := p.([]hooks.Policy); ok {
			activePolicies = append(activePolicies, policyList...)
		}
	}

	readOnly := make(map[antigravity.BuiltinTools]bool)
	for _, t := range antigravity.ReadOnlyBuiltinTools() {
		readOnly[t] = true
	}

	var activeBuiltinTools []antigravity.BuiltinTools
	if len(baseCfg.Capabilities.EnabledTools) > 0 {
		activeBuiltinTools = baseCfg.Capabilities.EnabledTools
	} else if len(baseCfg.Capabilities.DisabledTools) > 0 {
		disabled := make(map[antigravity.BuiltinTools]bool)
		for _, t := range baseCfg.Capabilities.DisabledTools {
			disabled[t] = true
		}
		allBuiltins := []antigravity.BuiltinTools{
			antigravity.BuiltinListDir, antigravity.BuiltinSearchDir, antigravity.BuiltinFindFile, antigravity.BuiltinViewFile,
			antigravity.BuiltinCreateFile, antigravity.BuiltinEditFile, antigravity.BuiltinRunCommand, antigravity.BuiltinAskQuestion,
			antigravity.BuiltinStartSubagent, antigravity.BuiltinGenerateImage, antigravity.BuiltinFinish,
		}
		for _, t := range allBuiltins {
			if !disabled[t] {
				activeBuiltinTools = append(activeBuiltinTools, t)
			}
		}
	} else {
		activeBuiltinTools = []antigravity.BuiltinTools{
			antigravity.BuiltinListDir, antigravity.BuiltinSearchDir, antigravity.BuiltinFindFile, antigravity.BuiltinViewFile,
			antigravity.BuiltinCreateFile, antigravity.BuiltinEditFile, antigravity.BuiltinRunCommand, antigravity.BuiltinAskQuestion,
			antigravity.BuiltinStartSubagent, antigravity.BuiltinGenerateImage, antigravity.BuiltinFinish,
		}
	}

	var hasWriteTools bool
	for _, t := range activeBuiltinTools {
		if !readOnly[t] {
			hasWriteTools = true
			break
		}
	}

	hasMcpServers := len(baseCfg.McpServers) > 0
	hasToolDecideHook := a.hookRunner.HasPreToolCallDecideHook()

	if (hasWriteTools || hasMcpServers) && len(activePolicies) == 0 && !hasToolDecideHook {
		return errors.New("write tools or MCP servers are enabled without a safety policy. Add policies=[policy.allow_all()] to approve all tool calls, or policies=[policy.deny_all(), policy.allow('tool_name')] to selectively allow specific tools")
	}

	if len(activePolicies) > 0 {
		a.hookRunner.RegisterHook(hooks.Enforce(activePolicies, baseCfg.McpServers))
	}

	// Parse custom tools
	var activeTools []tools.Tool
	for _, t := range baseCfg.Tools {
		if tool, ok := t.(tools.Tool); ok {
			activeTools = append(activeTools, tool)
		} else {
			fnVal := reflect.ValueOf(t)
			if fnVal.Kind() == reflect.Func {
				fullName := runtime.FuncForPC(fnVal.Pointer()).Name()
				parts := strings.Split(fullName, ".")
				name := parts[len(parts)-1]
				name = strings.Split(name, "-")[0]

				tool, err := tools.ToolFromFunc(name, "Custom tool "+name, t)
				if err != nil {
					return fmt.Errorf("failed to create tool from function %s: %w", fullName, err)
				}
				activeTools = append(activeTools, tool)
			} else {
				return fmt.Errorf("unsupported tool type: %T", t)
			}
		}
	}

	a.toolRunner = tools.NewToolRunner(activeTools)

	strategy, err := a.config.CreateStrategy(a.toolRunner, a.hookRunner)
	if err != nil {
		return fmt.Errorf("failed to create connection strategy: %w", err)
	}
	a.strategy = strategy

	if err := a.strategy.Start(ctx); err != nil {
		strategy.Close()
		return fmt.Errorf("failed to start connection strategy: %w", err)
	}

	conn, err := a.strategy.Connect()
	if err != nil {
		strategy.Close()
		return fmt.Errorf("failed to connect: %w", err)
	}

	if antigravity.NewConversation == nil {
		strategy.Close()
		return errors.New("conversation subsystem not initialized. Ensure conversation package is imported")
	}
	a.conversation = antigravity.NewConversation(conn)

	// Start triggers
	if len(a.pendingTrigs) > 0 {
		a.triggerRunner = triggers.NewTriggerRunner(a.pendingTrigs, conn)
		if err := a.triggerRunner.Start(ctx); err != nil {
			a.triggerRunner.Stop()
			strategy.Close()
			return fmt.Errorf("failed to start triggers: %w", err)
		}
		a.pendingTrigs = nil
	}

	// Wire ToolContext into ToolRunner
	if a.toolRunner != nil {
		a.toolRunner.SetContext(conn)
	}

	a.isStarted = true
	return nil
}

// Close stops the agent session, shutting down triggers, conversation channels, and connection strategies.
func (a *Agent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.isStarted {
		return nil
	}
	a.isStarted = false

	if a.triggerRunner != nil {
		a.triggerRunner.Stop()
	}

	var closeErr error
	if a.conversation != nil {
		if connCast, ok := a.conversation.Connection().(antigravity.Connection); ok {
			if err := connCast.Disconnect(context.Background()); err != nil {
				closeErr = err
			}
		}
	}

	if a.strategy != nil {
		if err := a.strategy.Close(); err != nil {
			if closeErr == nil {
				closeErr = err
			}
		}
	}

	return closeErr
}

// Chat sends a prompt and returns the final response from the agent.
func (a *Agent) Chat(ctx context.Context, prompt antigravity.Content) (*antigravity.ChatResponse, error) {
	conv := a.Conversation()
	if conv == nil {
		return nil, errors.New("agent session not started")
	}
	return conv.Chat(ctx, prompt)
}

// Conversation returns the active Conversation session.
// Returns nil if the agent session has not been started.
func (a *Agent) Conversation() antigravity.Conversation {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conversation
}

// ConversationID returns the conversation identifier assigned by the runtime.
// Returns empty string before the session starts.
func (a *Agent) ConversationID() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conversation == nil {
		return ""
	}
	if connCast, ok := a.conversation.Connection().(antigravity.Connection); ok {
		return connCast.ConversationID()
	}
	return ""
}

// IsStarted returns true if the agent session is active.
func (a *Agent) IsStarted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isStarted
}
