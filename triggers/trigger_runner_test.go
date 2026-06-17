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
	"sync"
	"testing"
	"time"

	"github.com/JulienBreux/antigravity-sdk-go"
)

type mockConnection struct {
	antigravity.Connection
	lastNotification string
	mu               sync.Mutex
}

func (m *mockConnection) SendTriggerNotification(ctx context.Context, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastNotification = content
	return nil
}

func (m *mockConnection) getLastNotification() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastNotification
}

func TestStartRunsTriggers(t *testing.T) {
	started := make(chan struct{})

	myTrigger := func(ctx context.Context, tc *TriggerContext) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	conn := &mockConnection{}
	runner := NewTriggerRunner([]Trigger{myTrigger}, conn)

	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	select {
	case <-started:
	case <-time.After(1 * time.Second):
		t.Fatal("Trigger did not start in time")
	}

	if !runner.IsRunning() {
		t.Error("Expected runner to be running")
	}

	runner.Stop()

	if runner.IsRunning() {
		t.Error("Expected runner to be stopped")
	}
}

func TestStopCancelsAllTriggers(t *testing.T) {
	var mu sync.Mutex
	cancelledCount := 0

	myTrigger := func(ctx context.Context, tc *TriggerContext) error {
		<-ctx.Done()
		mu.Lock()
		cancelledCount++
		mu.Unlock()
		return ctx.Err()
	}

	conn := &mockConnection{}
	runner := NewTriggerRunner([]Trigger{myTrigger, myTrigger}, conn)

	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	// Give them a moment to spawn and block on ctx.Done
	time.Sleep(10 * time.Millisecond)

	runner.Stop()

	mu.Lock()
	count := cancelledCount
	mu.Unlock()

	if count != 2 {
		t.Errorf("Expected 2 cancelled triggers, got %d", count)
	}
}

func TestExceptionInTriggerDoesNotCrashOthers(t *testing.T) {
	startedGood := make(chan struct{})

	badTrigger := func(ctx context.Context, tc *TriggerContext) error {
		return errors.New("boom")
	}

	goodTrigger := func(ctx context.Context, tc *TriggerContext) error {
		close(startedGood)
		<-ctx.Done()
		return ctx.Err()
	}

	conn := &mockConnection{}
	runner := NewTriggerRunner([]Trigger{badTrigger, goodTrigger}, conn)

	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start runner: %v", err)
	}

	select {
	case <-startedGood:
	case <-time.After(1 * time.Second):
		t.Fatal("Good trigger did not start in time")
	}

	if !runner.IsRunning() {
		t.Error("Runner should still be running even if one trigger fails")
	}

	runner.Stop()
}

func TestStartTwiceRaises(t *testing.T) {
	myTrigger := func(ctx context.Context, tc *TriggerContext) error {
		<-ctx.Done()
		return ctx.Err()
	}

	conn := &mockConnection{}
	runner := NewTriggerRunner([]Trigger{myTrigger}, conn)

	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to first start runner: %v", err)
	}

	if err := runner.Start(ctx); err == nil {
		t.Error("Expected error when starting twice, but got nil")
	}

	runner.Stop()
}

func TestStopWhenNotStartedIsNoop(t *testing.T) {
	conn := &mockConnection{}
	runner := NewTriggerRunner([]Trigger{}, conn)
	// Should not panic or hang
	runner.Stop()
}

func TestEmptyTriggersList(t *testing.T) {
	conn := &mockConnection{}
	runner := NewTriggerRunner([]Trigger{}, conn)

	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// It starts but since there are no triggers, it has nothing running.
	// But it is technically marked as running until stopped.
	if !runner.IsRunning() {
		t.Error("Expected runner to be running (as active session)")
	}

	runner.Stop()
	if runner.IsRunning() {
		t.Error("Expected runner to be stopped")
	}
}

func TestTriggerReceivesContextAndCanSend(t *testing.T) {
	sent := make(chan struct{})

	myTrigger := func(ctx context.Context, tc *TriggerContext) error {
		if err := tc.Send(ctx, "hello from trigger"); err != nil {
			return err
		}
		close(sent)
		return nil
	}

	conn := &mockConnection{}
	runner := NewTriggerRunner([]Trigger{myTrigger}, conn)

	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	select {
	case <-sent:
	case <-time.After(1 * time.Second):
		t.Fatal("Trigger did not send notification in time")
	}

	runner.Stop()

	if got := conn.getLastNotification(); got != "hello from trigger" {
		t.Errorf("Expected notification 'hello from trigger', got %q", got)
	}
}

func TestEveryTrigger(t *testing.T) {
	var mu sync.Mutex
	tickCount := 0

	cb := func(ctx context.Context, tc *TriggerContext) error {
		mu.Lock()
		tickCount++
		mu.Unlock()
		return nil
	}

	// run callback every 5 milliseconds
	trg := Every(5*time.Millisecond, cb)

	conn := &mockConnection{}
	runner := NewTriggerRunner([]Trigger{trg}, conn)

	ctx := context.Background()
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	runner.Stop()

	mu.Lock()
	count := tickCount
	mu.Unlock()

	if count < 2 {
		t.Errorf("Expected at least 2 ticks, got %d", count)
	}
}
