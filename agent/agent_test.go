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
	"sync"
	"testing"
	"time"

	"github.com/JulienBreux/antigravity-sdk-go"
	"github.com/JulienBreux/antigravity-sdk-go/hooks"
	"github.com/JulienBreux/antigravity-sdk-go/triggers"
)

// Mock strategy and connection
type mockAgentConfig struct {
	antigravity.BaseAgentConfig
	startErr error
}

func (m *mockAgentConfig) CreateStrategy(toolRunner any, hookRunner any) (antigravity.ConnectionStrategy, error) {
	return &mockStrategy{
		startErr: m.startErr,
	}, nil
}

type mockStrategy struct {
	startErr error
	closed   bool
}

func (s *mockStrategy) Start(ctx context.Context) error {
	return s.startErr
}

func (s *mockStrategy) Connect() (antigravity.Connection, error) {
	return &mockConn{}, nil
}

func (s *mockStrategy) Close() error {
	s.closed = true
	return nil
}

type mockConn struct {
	antigravity.Connection
	disconnected bool
}

func (c *mockConn) IsIdle() bool {
	return true
}

func (c *mockConn) ConversationID() string {
	return "mock-id"
}

func (c *mockConn) Disconnect(ctx context.Context) error {
	c.disconnected = true
	return nil
}

func (c *mockConn) Send(ctx context.Context, prompt antigravity.Content) error {
	return nil
}

func (c *mockConn) ReceiveSteps(ctx context.Context) <-chan *antigravity.Step {
	ch := make(chan *antigravity.Step)
	close(ch)
	return ch
}

func TestAgentStartAndClose(t *testing.T) {
	config := &mockAgentConfig{}
	config.Capabilities = antigravity.CapabilitiesConfig{
		EnabledTools: []antigravity.BuiltinTools{antigravity.BuiltinViewFile},
	}

	agent := NewAgent(config)
	if agent.IsStarted() {
		t.Error("Expected agent not to be started initially")
	}

	ctx := context.Background()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Failed to start agent: %v", err)
	}

	if !agent.IsStarted() {
		t.Error("Expected agent to be started after Start()")
	}

	if id := agent.ConversationID(); id != "mock-id" {
		t.Errorf("Expected conversation ID 'mock-id', got %q", id)
	}

	if err := agent.Close(); err != nil {
		t.Fatalf("Failed to close agent: %v", err)
	}

	if agent.IsStarted() {
		t.Error("Expected agent to be stopped after Close()")
	}
}

func TestAgentSafetyPolicyEnforcement(t *testing.T) {
	config := &mockAgentConfig{}
	config.Capabilities = antigravity.CapabilitiesConfig{
		EnabledTools: []antigravity.BuiltinTools{antigravity.BuiltinRunCommand},
	}

	agent := NewAgent(config)
	ctx := context.Background()
	err := agent.Start(ctx)
	if err == nil {
		agent.Close()
		t.Fatal("Expected error starting agent with write tools but no policies/hooks, got nil")
	}
}

type dummyPreToolCallDecide struct{}

func (dummyPreToolCallDecide) Run(ctx *hooks.HookContext, call antigravity.ToolCall) (antigravity.HookResult, error) {
	return antigravity.HookResult{Allow: true}, nil
}

func TestAgentWithPreToolCallDecideHook(t *testing.T) {
	config := &mockAgentConfig{}
	config.Capabilities = antigravity.CapabilitiesConfig{
		EnabledTools: []antigravity.BuiltinTools{antigravity.BuiltinRunCommand},
	}

	agent := NewAgent(config)
	agent.RegisterHook(dummyPreToolCallDecide{})

	ctx := context.Background()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Failed to start agent with custom decide hook: %v", err)
	}
	defer agent.Close()
}

func TestAgentWithTriggers(t *testing.T) {
	config := &mockAgentConfig{}
	config.Capabilities = antigravity.CapabilitiesConfig{
		EnabledTools: []antigravity.BuiltinTools{antigravity.BuiltinViewFile},
	}

	agent := NewAgent(config)

	var mu sync.Mutex
	triggerRan := false

	trig := func(ctx context.Context, tc *triggers.TriggerContext) error {
		mu.Lock()
		triggerRan = true
		mu.Unlock()
		return nil
	}

	if err := agent.RegisterTrigger(trig); err != nil {
		t.Fatalf("Failed to register trigger: %v", err)
	}

	ctx := context.Background()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Failed to start agent: %v", err)
	}
	defer agent.Close()

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	ran := triggerRan
	mu.Unlock()

	if !ran {
		t.Error("Expected trigger to run")
	}
}

func TestAgentRegisterTriggerAfterStartFails(t *testing.T) {
	config := &mockAgentConfig{}
	config.Capabilities = antigravity.CapabilitiesConfig{
		EnabledTools: []antigravity.BuiltinTools{antigravity.BuiltinViewFile},
	}

	agent := NewAgent(config)
	ctx := context.Background()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer agent.Close()

	trig := func(ctx context.Context, tc *triggers.TriggerContext) error {
		return nil
	}

	if err := agent.RegisterTrigger(trig); err == nil {
		t.Error("Expected error registering trigger after agent start, got nil")
	}
}

// Test function reflection tool wrapping
type customArgs struct {
	Param string `json:"param"`
}

func myCustomTool(ctx context.Context, args customArgs) (string, error) {
	return "processed: " + args.Param, nil
}

func TestAgentWithCustomToolFunc(t *testing.T) {
	config := &mockAgentConfig{}
	config.Capabilities = antigravity.CapabilitiesConfig{
		EnabledTools: []antigravity.BuiltinTools{antigravity.BuiltinViewFile},
	}
	config.Tools = []any{myCustomTool}

	agent := NewAgent(config)
	ctx := context.Background()
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("Failed to start agent with custom tool function: %v", err)
	}
	defer agent.Close()

	if agent.toolRunner == nil {
		t.Fatal("Expected tool runner to be initialized")
	}
}
