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

	"github.com/JulienBreux/antigravity-sdk-go"
	"github.com/JulienBreux/antigravity-sdk-go/agent"
	"github.com/JulienBreux/antigravity-sdk-go/connections/local"
	_ "github.com/JulienBreux/antigravity-sdk-go/conversation"
	"github.com/JulienBreux/antigravity-sdk-go/hooks"
)

func main() {
	ctx := context.Background()

	// 1. Initialize local configuration
	config := local.NewLocalAgentConfig()
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("Warning: GEMINI_API_KEY environment variable is not set")
	} else {
		config.APIKey = &apiKey
	}

	// Safety policy: allow all tool calls for this simple demo
	config.Policies = []any{hooks.AllowAll()}

	// 2. Create the Agent
	myAgent := agent.NewAgent(config)

	// 3. Start the agent session
	if err := myAgent.Start(ctx); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}
	defer myAgent.Close()

	// 4. Send a simple prompt to the agent
	promptStr := "Say 'Hello World!'"
	fmt.Printf("User: %s\n", promptStr)

	prompt := antigravity.Content{
		antigravity.StringContent(promptStr),
	}

	response, err := myAgent.Chat(ctx, prompt)
	if err != nil {
		log.Fatalf("Chat error: %v", err)
	}

	// 5. Stream the response tokens as they arrive
	fmt.Print("Agent: ")
	for chunk := range response.Chunks() {
		if txt, ok := chunk.(antigravity.Text); ok {
			fmt.Print(txt.Text)
		}
	}
	fmt.Println()
}
