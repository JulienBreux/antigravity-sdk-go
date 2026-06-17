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
	"strings"

	"github.com/JulienBreux/antigravity-sdk-go"
	"github.com/JulienBreux/antigravity-sdk-go/agent"
	"github.com/JulienBreux/antigravity-sdk-go/connections/local"
	_ "github.com/JulienBreux/antigravity-sdk-go/conversation"
	"github.com/JulienBreux/antigravity-sdk-go/hooks"
	"github.com/JulienBreux/antigravity-sdk-go/tools"
)

// FruitSkuArgs represents arguments for lookupFruitSku tool.
type FruitSkuArgs struct {
	FruitName string `json:"fruit_name" description:"The name of the fruit to look up (e.g. apple, banana)"`
}

// 1. Simple lookup tool
func lookupFruitSku(ctx context.Context, args FruitSkuArgs) (string, error) {
	skus := map[string]string{
		"apple":  "SKU-APP-123",
		"banana": "SKU-BAN-456",
		"orange": "SKU-ORA-789",
	}
	name := strings.ToLower(args.FruitName)
	if strings.HasSuffix(name, "s") {
		name = strings.TrimSuffix(name, "s")
	}
	sku, ok := skus[name]
	if !ok {
		sku = "SKU-GEN-000"
	}
	return fmt.Sprintf("SKU for %s is %s. Order ID for restocking: ORD-%s-NEW", args.FruitName, sku, sku), nil
}

// RecordFruitArgs represents arguments for recordFruit tool.
type RecordFruitArgs struct {
	Sku   string `json:"sku" description:"The SKU of the fruit"`
	Count int    `json:"count" description:"The quantity of the fruit to record"`
}

// 2. Stateful tool using *tools.ToolContext
func recordFruit(ctx context.Context, toolCtx *tools.ToolContext, args RecordFruitArgs) (string, error) {
	// Retrieve current counts from tool state, or initialize
	countsRaw := toolCtx.GetState("fruit_counts", nil)
	counts := make(map[string]int)
	if countsRaw != nil {
		if cMap, ok := countsRaw.(map[string]int); ok {
			for k, v := range cMap {
				counts[k] = v
			}
		}
	}

	counts[args.Sku] += args.Count
	toolCtx.SetState("fruit_counts", counts)

	total := counts[args.Sku]
	return fmt.Sprintf("Recorded %d units for SKU %s. Total count is now %d.", args.Count, args.Sku, total), nil
}

func main() {
	ctx := context.Background()

	config := local.NewLocalAgentConfig()
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey != "" {
		config.APIKey = &apiKey
	}

	// Register tools
	config.Tools = []any{lookupFruitSku, recordFruit}

	// Set instructions forcing the agent to use both tools
	config.SystemInstructions = antigravity.CustomSystemInstructions{
		Text: "You keep track of fruit inventory. To record fruits, you MUST first look up the fruit's SKU using lookupFruitSku, and then use that SKU with recordFruit.",
	}

	// Restrict tools for safety
	config.Policies = []any{
		hooks.DenyAll(),
		hooks.Allow("lookupFruitSku", nil, "allow_lookup"),
		hooks.Allow("recordFruit", nil, "allow_record"),
	}

	myAgent := agent.NewAgent(config)
	if err := myAgent.Start(ctx); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}
	defer myAgent.Close()

	fmt.Println("=== Custom Tools Demo ===")

	// Turn 1
	prompt1 := "What is the SKU for apples? We need to order more."
	fmt.Printf("\nUser: %s\n", prompt1)
	resp1, err := myAgent.Chat(ctx, antigravity.Content{antigravity.StringContent(prompt1)})
	if err != nil {
		log.Fatalf("Chat 1 error: %v", err)
	}
	txt1, _ := resp1.Text()
	fmt.Printf("Agent: %s\n", txt1)

	// Turn 2
	prompt2 := "I have 5 apples."
	fmt.Printf("\nUser: %s\n", prompt2)
	resp2, err := myAgent.Chat(ctx, antigravity.Content{antigravity.StringContent(prompt2)})
	if err != nil {
		log.Fatalf("Chat 2 error: %v", err)
	}
	txt2, _ := resp2.Text()
	fmt.Printf("Agent: %s\n", txt2)

	// Turn 3
	prompt3 := "Oh, and another 3 apples."
	fmt.Printf("\nUser: %s\n", prompt3)
	resp3, err := myAgent.Chat(ctx, antigravity.Content{antigravity.StringContent(prompt3)})
	if err != nil {
		log.Fatalf("Chat 3 error: %v", err)
	}
	txt3, _ := resp3.Text()
	fmt.Printf("Agent: %s\n", txt3)
}
