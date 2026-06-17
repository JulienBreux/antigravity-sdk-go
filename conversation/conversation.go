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

package conversation

import (
	"context"
	"sync"

	"github.com/JulienBreux/antigravity-sdk-go"
)

type Conversation struct {
	conn              antigravity.Connection
	maxHistorySize    int
	steps             []antigravity.Step
	turnStartIndices  []int
	compactionIndices []int
	cumulativeUsage   antigravity.UsageMetadata
	turnUsage         *antigravity.UsageMetadata
	mu                sync.Mutex
}

func init() {
	antigravity.NewConversation = func(conn any) antigravity.Conversation {
		return &Conversation{
			conn:           conn.(antigravity.Connection),
			maxHistorySize: 10000,
		}
	}
}

func (c *Conversation) History() []antigravity.Step {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]antigravity.Step, len(c.steps))
	copy(out, c.steps)
	return out
}

func (c *Conversation) LastResponse() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.steps) - 1; i >= 0; i-- {
		s := c.steps[i]
		if s.IsCompleteResponse != nil && *s.IsCompleteResponse {
			return s.Content
		}
	}
	return ""
}

func (c *Conversation) TurnCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.turnStartIndices)
}

func (c *Conversation) CompactionIndices() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, len(c.compactionIndices))
	copy(out, c.compactionIndices)
	return out
}

func (c *Conversation) ClearHistory() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = nil
	c.turnStartIndices = nil
	c.compactionIndices = nil
	c.cumulativeUsage = antigravity.UsageMetadata{}
	c.turnUsage = nil
}

func (c *Conversation) GetLastStructuredOutput() any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.steps) - 1; i >= 0; i-- {
		s := c.steps[i]
		if s.Type == antigravity.StepTypeFinish {
			return s.StructuredOutput
		}
	}
	return nil
}

func (c *Conversation) CumulativeUsage() antigravity.UsageMetadata {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cumulativeUsage
}

func (c *Conversation) TurnUsage() *antigravity.UsageMetadata {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnUsage
}

func (c *Conversation) Cancel(ctx context.Context) error {
	return c.conn.Cancel(ctx)
}

func (c *Conversation) Chat(ctx context.Context, prompt antigravity.Content) (*antigravity.ChatResponse, error) {
	if !c.conn.IsIdle() {
		// Drain steps
		stepsChan := c.conn.ReceiveSteps(ctx)
		for range stepsChan {
			// drain
		}
	}

	c.mu.Lock()
	c.turnStartIndices = append(c.turnStartIndices, len(c.steps))
	c.turnUsage = nil
	c.mu.Unlock()

	if err := c.conn.Send(ctx, prompt); err != nil {
		return nil, err
	}

	chunksChan := make(chan antigravity.StreamChunk, 100)
	go func() {
		defer close(chunksChan)
		stepsChan := c.conn.ReceiveSteps(ctx)
		seenToolIDs := make(map[string]bool)

		for step := range stepsChan {
			c.mu.Lock()
			c.steps = append(c.steps, *step)
			idx := len(c.steps) - 1
			if step.Type == antigravity.StepTypeCompaction {
				c.compactionIndices = append(c.compactionIndices, idx)
			}
			if step.UsageMetadata != nil {
				c.accumulateUsage(step.UsageMetadata)
			}
			c.enforceMaxHistory()
			c.mu.Unlock()

			// Yield text deltas
			if step.Source == antigravity.StepSourceModel && step.Target == antigravity.StepTargetUser {
				if step.ThinkingDelta != "" {
					chunksChan <- antigravity.Thought{
						StepIndex: step.StepIndex,
						Text:      step.ThinkingDelta,
					}
				}
				if step.ContentDelta != "" {
					chunksChan <- antigravity.Text{
						StepIndex: step.StepIndex,
						Text:      step.ContentDelta,
					}
				}
			}

			// Yield tool calls
			if len(step.ToolCalls) > 0 {
				for _, tc := range step.ToolCalls {
					id := ""
					if tc.ID != nil {
						id = *tc.ID
					}
					if id == "" || !seenToolIDs[id] {
						if id != "" {
							seenToolIDs[id] = true
						}
						chunksChan <- tc
					}
				}
			}
		}
	}()

	return antigravity.NewChatResponse(chunksChan, c), nil
}

func (c *Conversation) accumulateUsage(um *antigravity.UsageMetadata) {
	if um == nil {
		return
	}
	if c.turnUsage == nil {
		c.turnUsage = &antigravity.UsageMetadata{}
	}
	addUsage(c.turnUsage, um)
	addUsage(&c.cumulativeUsage, um)
}

func addUsage(target *antigravity.UsageMetadata, source *antigravity.UsageMetadata) {
	target.PromptTokenCount = addUint64(target.PromptTokenCount, source.PromptTokenCount)
	target.CachedContentTokenCount = addUint64(target.CachedContentTokenCount, source.CachedContentTokenCount)
	target.CandidatesTokenCount = addUint64(target.CandidatesTokenCount, source.CandidatesTokenCount)
	target.ThoughtsTokenCount = addUint64(target.ThoughtsTokenCount, source.ThoughtsTokenCount)
	target.TotalTokenCount = addUint64(target.TotalTokenCount, source.TotalTokenCount)
}

func addUint64(a, b *uint64) *uint64 {
	var val uint64
	if a != nil {
		val += *a
	}
	if b != nil {
		val += *b
	}
	return &val
}

func (c *Conversation) enforceMaxHistory() {
	if c.maxHistorySize > 0 && len(c.steps) > c.maxHistorySize {
		overflow := len(c.steps) - c.maxHistorySize
		c.steps = c.steps[overflow:]

		var newTurns []int
		for _, idx := range c.turnStartIndices {
			if idx >= overflow {
				newTurns = append(newTurns, idx-overflow)
			}
		}
		c.turnStartIndices = newTurns

		var newCompactions []int
		for _, idx := range c.compactionIndices {
			if idx >= overflow {
				newCompactions = append(newCompactions, idx-overflow)
			}
		}
		c.compactionIndices = newCompactions
	}
}

// Connection returns the underlying connection.
func (c *Conversation) Connection() any {
	return c.conn
}
