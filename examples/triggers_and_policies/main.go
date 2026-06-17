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

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JulienBreux/antigravity-sdk-go"
	"github.com/JulienBreux/antigravity-sdk-go/agent"
	"github.com/JulienBreux/antigravity-sdk-go/connections/local"
	_ "github.com/JulienBreux/antigravity-sdk-go/conversation"
	"github.com/JulienBreux/antigravity-sdk-go/hooks"
	"github.com/JulienBreux/antigravity-sdk-go/triggers"
)

var (
	ticketCounter int
	mu            sync.Mutex
	// standbyActive gates the trigger: it only sends alerts after the first
	// chat turn has completed, matching the Python SDK's pattern.
	standbyActive atomic.Bool
)

// A periodic trigger that checks for tickets and alerts the agent.
// Only fires after standbyActive is set to true (after the first chat turn).
func pollTicketQueue(ctx context.Context, tc *triggers.TriggerContext) error {
	if !standbyActive.Load() {
		return nil // Not yet in standby mode
	}

	mu.Lock()
	ticketCounter++
	current := ticketCounter
	mu.Unlock()

	// Trigger alert on the second tick
	if current == 2 {
		fmt.Println("\n[TRIGGER EVENT] SRE Ticket Queue Alert fired!")
		return tc.Send(ctx, "[SYSTEM ALERT] New critical ticket assigned: b/98765. Title: Database Connection Leak in Prod.")
	}
	return nil
}

// Ask user confirmation before executing run_command tool calls
func askUserConfirmation(ctx context.Context, call antigravity.ToolCall) (bool, error) {
	fmt.Printf("\n[PROPOSAL] SRE Command requested: %v\n", call.Args["CommandLine"])
	fmt.Print("Confirm execution? (y/n): ")
	var answer string
	fmt.Scanln(&answer)
	return answer == "y" || answer == "yes", nil
}

func main() {
	ctx := context.Background()

	config := local.NewLocalAgentConfig()
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey != "" {
		config.APIKey = &apiKey
	}

	// 1. Enable write capabilities so command tool is available
	config.Capabilities.EnabledTools = []antigravity.BuiltinTools{
		antigravity.BuiltinViewFile,
		antigravity.BuiltinRunCommand,
		antigravity.BuiltinListDir,
	}

	// 2. Set safety policies: require confirmation on run_command
	config.Policies = []any{
		hooks.AskUser(string(antigravity.BuiltinRunCommand), askUserConfirmation, nil, "confirm_run_cmd"),
		hooks.AllowAll(),
	}

	// 3. Register SRE ticket poll trigger (every 1 second for demo)
	config.Triggers = []any{
		triggers.Every(1*time.Second, pollTicketQueue),
	}

	// Use the workspace directory in the instructions so the model knows where to look
	cwd, _ := os.Getwd()
	config.SystemInstructions = antigravity.CustomSystemInstructions{
		Text: fmt.Sprintf(
			"You are an SRE operations assistant. You monitor a queue of incoming "+
				"support tickets. When the user asks for updates, report any tickets "+
				"that came in from the background system alert trigger. "+
				"Your workspace is: %s. Only operate within this directory. "+
				"Use run_command to run diagnostic commands when needed.", cwd),
	}

	myAgent := agent.NewAgent(config)
	if err := myAgent.Start(ctx); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}
	defer myAgent.Close()

	fmt.Println("=== Triggers & Safety Policies Demo ===")

	// Turn 1: Initialize the conversation with the agent BEFORE enabling triggers.
	// The localharness requires at least one chat turn to initialize the conversation
	// state before it can accept trigger notifications.
	prompt1 := "Your task will be to standby and simply let me know if there are any critical tickets received."
	fmt.Printf("\nUser: %s\n", prompt1)
	resp1, err := myAgent.Chat(ctx, antigravity.Content{antigravity.StringContent(prompt1)})
	if err != nil {
		log.Fatalf("Chat error: %v", err)
	}
	txt1, _ := resp1.Text()
	fmt.Printf("Agent: %s\n", txt1)

	// Now enable the standby trigger after the first turn has completed
	fmt.Println("\nStandby mode activated. Waiting for ticket events...")
	standbyActive.Store(true)

	// Sleep to let background SRE ticket trigger fire and notify the agent
	time.Sleep(3 * time.Second)

	// Turn 2: User asks agent to handle any alerts
	prompt2 := "I'm back. Did anything critical come in while I was working?"
	fmt.Printf("\nUser: %s\n", prompt2)

	resp2, err := myAgent.Chat(ctx, antigravity.Content{antigravity.StringContent(prompt2)})
	if err != nil {
		log.Fatalf("Chat error: %v", err)
	}

	txt2, _ := resp2.Text()
	fmt.Printf("Agent: %s\n", txt2)
}

