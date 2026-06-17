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

package antigravity

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultModel                = "gemini-3.5-flash"
	DefaultImageGenerationModel = "gemini-3.1-flash-image-preview"
)

// ThinkingLevel for Gemini models that support extended thinking.
type ThinkingLevel string

const (
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
)

// GenerationConfig contains generation parameters for a model.
type GenerationConfig struct {
	ThinkingLevel *ThinkingLevel
}

// ModelEntry is a model with optional auth and generation overrides.
type ModelEntry struct {
	Name       string
	APIKey     *string
	Generation GenerationConfig
}

// ModelConfig is the model selection for each capability.
type ModelConfig struct {
	Default         ModelEntry
	ImageGeneration ModelEntry
}

func NewDefaultModelConfig() ModelConfig {
	return ModelConfig{
		Default:         ModelEntry{Name: DefaultModel},
		ImageGeneration: ModelEntry{Name: DefaultImageGenerationModel},
	}
}

// GeminiConfig is the configuration for the Gemini model backend.
type GeminiConfig struct {
	APIKey   *string
	Vertex   bool
	Project  *string
	Location *string
	Models   ModelConfig
}

// SystemInstructionSection is a named section to append to the system instructions.
type SystemInstructionSection struct {
	Content string
	Title   string
}

// CustomSystemInstructions replaces the default system instructions entirely.
type CustomSystemInstructions struct {
	Text string
}

func (CustomSystemInstructions) isSystemInstructions() {}

// TemplatedSystemInstructions overrides the agent identity and appends sections.
type TemplatedSystemInstructions struct {
	Identity string
	Sections []SystemInstructionSection
}

func (TemplatedSystemInstructions) isSystemInstructions() {}

// SystemInstructions is the interface representing either CustomSystemInstructions or TemplatedSystemInstructions.
type SystemInstructions interface {
	isSystemInstructions()
}

// BuiltinTools identifiers for common connection-provided builtin tools.
type BuiltinTools string

const (
	BuiltinListDir       BuiltinTools = "list_directory"
	BuiltinSearchDir     BuiltinTools = "search_directory"
	BuiltinFindFile      BuiltinTools = "find_file"
	BuiltinViewFile      BuiltinTools = "view_file"
	BuiltinCreateFile    BuiltinTools = "create_file"
	BuiltinEditFile      BuiltinTools = "edit_file"
	BuiltinRunCommand    BuiltinTools = "run_command"
	BuiltinAskQuestion   BuiltinTools = "ask_question"
	BuiltinStartSubagent BuiltinTools = "start_subagent"
	BuiltinGenerateImage BuiltinTools = "generate_image"
	BuiltinFinish        BuiltinTools = "finish"
)

func ReadOnlyBuiltinTools() []BuiltinTools {
	return []BuiltinTools{
		BuiltinListDir,
		BuiltinSearchDir,
		BuiltinFindFile,
		BuiltinViewFile,
		BuiltinFinish,
	}
}

func NondestructiveBuiltinTools() []BuiltinTools {
	return []BuiltinTools{
		BuiltinListDir,
		BuiltinSearchDir,
		BuiltinFindFile,
		BuiltinViewFile,
		BuiltinCreateFile,
		BuiltinEditFile,
		BuiltinAskQuestion,
		BuiltinStartSubagent,
		BuiltinGenerateImage,
		BuiltinFinish,
	}
}

func FileBuiltinTools() []BuiltinTools {
	return []BuiltinTools{
		BuiltinViewFile,
		BuiltinCreateFile,
		BuiltinEditFile,
	}
}

// CapabilitiesConfig general agent capability configuration.
type CapabilitiesConfig struct {
	EnableSubagents       bool
	EnabledTools          []BuiltinTools
	DisabledTools         []BuiltinTools
	CompactionThreshold   *int
	ImageModel            string
	FinishToolSchemaJSON  *string
}

// BaseMcpServerConfig contains common configuration fields for all MCP servers.
type BaseMcpServerConfig struct {
	Name           string
	TimeoutSeconds *int
}

// McpStdioServer is the configuration for an MCP server connected via stdio.
type McpStdioServer struct {
	BaseMcpServerConfig
	Command       string
	Args          []string
	Env           map[string]string
	EnabledTools  []string
	DisabledTools []string
}

func (McpStdioServer) isMcpServerConfig() {}

// McpStreamableHttpServer is the configuration for an MCP server connected via Http/SSE.
type McpStreamableHttpServer struct {
	BaseMcpServerConfig
	URL              string
	Headers          map[string]string
	TimeoutSeconds   float64
	SSEReadTimeout   float64
	TerminateOnClose bool
	EnabledTools     []string
	DisabledTools    []string
}

func (McpStreamableHttpServer) isMcpServerConfig() {}

// GetName returns the name of the MCP server.
func (b BaseMcpServerConfig) GetName() string {
	return b.Name
}

// GetTimeoutSeconds returns the timeout of the MCP server in seconds.
func (b BaseMcpServerConfig) GetTimeoutSeconds() *int {
	return b.TimeoutSeconds
}

// McpServerConfig represents an MCP server configuration (Stdio or Http).
type McpServerConfig interface {
	isMcpServerConfig()
	GetName() string
	GetTimeoutSeconds() *int
}

// ToolCall represents a tool call.
type ToolCall struct {
	Name          string         `json:"name"`
	Args          map[string]any `json:"args"`
	ID            *string        `json:"id,omitempty"`
	CanonicalPath *string        `json:"canonical_path,omitempty"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	Name      string         `json:"name"`
	ID        *string        `json:"id,omitempty"`
	Result    any            `json:"result,omitempty"`
	Error     *string        `json:"error,omitempty"`
	Exception error          `json:"-"`
}

// UsageMetadata contains token usage counters.
type UsageMetadata struct {
	PromptTokenCount        *uint64
	CachedContentTokenCount *uint64
	CandidatesTokenCount    *uint64
	ThoughtsTokenCount      *uint64
	TotalTokenCount         *uint64
}

// StepType represents the high-level type of a step.
type StepType string

const (
	StepTypeTextResponse  StepType = "TEXT_RESPONSE"
	StepTypeToolCall      StepType = "TOOL_CALL"
	StepTypeSystemMessage StepType = "SYSTEM_MESSAGE"
	StepTypeCompaction    StepType = "COMPACTION"
	StepTypeFinish        StepType = "FINISH"
	StepTypeUnknown       StepType = "UNKNOWN"
)

// StepSource represents the source that generated the step.
type StepSource string

const (
	StepSourceSystem  StepSource = "SYSTEM"
	StepSourceUser    StepSource = "USER"
	StepSourceModel   StepSource = "MODEL"
	StepSourceUnknown StepSource = "UNKNOWN"
)

// StepTarget represents the target interacting with the step.
type StepTarget string

const (
	StepTargetUser        StepTarget = "TARGET_USER"
	StepTargetEnvironment StepTarget = "TARGET_ENVIRONMENT"
	StepTargetUnspecified StepTarget = "TARGET_UNSPECIFIED"
	StepTargetUnknown     StepTarget = "UNKNOWN"
)

// StepStatus represents the status of a step.
type StepStatus string

const (
	StepStatusActive         StepStatus = "ACTIVE"
	StepStatusDone           StepStatus = "DONE"
	StepStatusWaitingForUser StepStatus = "WAITING_FOR_USER"
	StepStatusError          StepStatus = "ERROR"
	StepStatusCanceled       StepStatus = "CANCELED"
	StepStatusUnknown        StepStatus = "UNKNOWN"
)

// Step represents a single event in the agent trajectory.
type Step struct {
	ID                 string
	StepIndex          int
	Type               StepType
	Source             StepSource
	Target             StepTarget
	Status             StepStatus
	Content            string
	ContentDelta       string
	Thinking           string
	ThinkingDelta      string
	ToolCalls          []ToolCall
	Error              string
	IsCompleteResponse *bool
	StructuredOutput   any
	UsageMetadata      *UsageMetadata
}

// HookResult represents the decision of a hook.
type HookResult struct {
	Allow   bool
	Message string
}

// QuestionResponse represents an individual response to an AskQuestion question.
type QuestionResponse struct {
	SelectedOptionIDs []string
	FreeformResponse  string
	Skipped           bool
}

// QuestionHookResult contains the results of a question prompt session.
type QuestionHookResult struct {
	Responses []QuestionResponse
	Cancelled bool
}

// AskQuestionOption option for a multiple-choice question.
type AskQuestionOption struct {
	ID   string
	Text string
}

// AskQuestionEntry a single question definition.
type AskQuestionEntry struct {
	Question      string
	Options       []AskQuestionOption
	IsMultiSelect bool
}

// AskQuestionInteractionSpec the list of questions for a clarifying question flow.
type AskQuestionInteractionSpec struct {
	Questions []AskQuestionEntry
}

// TriggerDelivery controls how trigger messages are delivered.
type TriggerDelivery string

const (
	TriggerDeliverySendImmediately TriggerDelivery = "send_immediately"
	TriggerDeliveryWaitIdle        TriggerDelivery = "wait_idle"
)

// FileChangeKind represents the filesystem event type.
type FileChangeKind string

const (
	FileChangeAdded    FileChangeKind = "added"
	FileChangeModified FileChangeKind = "modified"
	FileChangeDeleted  FileChangeKind = "deleted"
)

// FileChange a single filesystem event.
type FileChange struct {
	Kind FileChangeKind
	Path string
}

// StreamChunk is the interface for real-time semantic chunks.
type StreamChunk interface {
	isStreamChunk()
	GetStepIndex() int
}

// Thought delta reasoning token.
type Thought struct {
	StepIndex int
	Text      string
	Signature []byte
}

func (Thought) isStreamChunk()        {}
func (t Thought) GetStepIndex() int { return t.StepIndex }

// Text delta output token.
type Text struct {
	StepIndex int
	Text      string
}

func (Text) isStreamChunk()        {}
func (t Text) GetStepIndex() int { return t.StepIndex }

// ContentPrimitive represents a single piece of user input.
type ContentPrimitive interface {
	isContentPrimitive()
}

type StringContent string

func (StringContent) isContentPrimitive() {}

type Media interface {
	ContentPrimitive
	GetData() []byte
	GetMimeType() string
	GetDescription() string
}

type BaseMedia struct {
	Data        []byte
	MimeType    string
	Description string
}

func (BaseMedia) isContentPrimitive() {}
func (m BaseMedia) GetData() []byte   { return m.Data }
func (m BaseMedia) GetMimeType() string {
	return m.MimeType
}
func (m BaseMedia) GetDescription() string {
	return m.Description
}

type Image struct{ BaseMedia }
type Document struct{ BaseMedia }
type Audio struct{ BaseMedia }
type Video struct{ BaseMedia }

type BuiltinSlashCommandName string

const (
	SlashCommandPlan BuiltinSlashCommandName = "plan"
)

type SlashCommand struct {
	Name BuiltinSlashCommandName
}

func (SlashCommand) isContentPrimitive() {}

// Content input primitive or sequence of primitives.
type Content []ContentPrimitive

// Helper function to load media from a file path.
func FromFile(path string, description string) (Media, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	ext := filepath.Ext(path)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return nil, fmt.Errorf("could not infer mime type for extension %q", ext)
	}

	bm := BaseMedia{
		Data:        data,
		MimeType:    mimeType,
		Description: description,
	}

	// We can classify by checking mime prefix or using a hardcoded map.
	// For simplicity, we can do prefix check.
	// Standard prefixes: image/, video/, audio/, or application/ (pdf/json)
	if len(mimeType) >= 6 && mimeType[:6] == "image/" {
		return Image{bm}, nil
	} else if len(mimeType) >= 6 && mimeType[:6] == "video/" {
		return Video{bm}, nil
	} else if len(mimeType) >= 6 && mimeType[:6] == "audio/" {
		return Audio{bm}, nil
	}
	// Fallback to Document
	return Document{bm}, nil
}

// Conversation represents a stateful session with the agent.
type Conversation interface {
	History() []Step
	LastResponse() string
	TurnCount() int
	CompactionIndices() []int
	ClearHistory()
	GetLastStructuredOutput() any
	CumulativeUsage() UsageMetadata
	TurnUsage() *UsageMetadata
	Cancel(ctx context.Context) error
	Chat(ctx context.Context, prompt Content) (*ChatResponse, error)
	Connection() any
}

// ConversationFactory is the factory function for creating conversations.
type ConversationFactory func(conn any) Conversation

// NewConversation is the registered factory to create conversations.
var NewConversation ConversationFactory

// ChatResponse wraps the real-time chunk stream from Agent.Chat.
type ChatResponse struct {
	chunks chan StreamChunk
	conv   Conversation
	err    error
	resolved []StreamChunk
	isDone   bool
	mu       sync.Mutex
}

func NewChatResponse(chunks chan StreamChunk, conv Conversation) *ChatResponse {
	return &ChatResponse{
		chunks: chunks,
		conv:   conv,
	}
}

func (r *ChatResponse) Chunks() <-chan StreamChunk {
	out := make(chan StreamChunk, 100)
	go func() {
		defer close(out)
		r.mu.Lock()
		// Yield already resolved chunks
		for _, c := range r.resolved {
			out <- c
		}
		if r.isDone {
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()

		for c := range r.chunks {
			r.mu.Lock()
			r.resolved = append(r.resolved, c)
			r.mu.Unlock()
			out <- c
		}
		r.mu.Lock()
		r.isDone = true
		r.mu.Unlock()
	}()
	return out
}

func (r *ChatResponse) Text() (string, error) {
	var sb strings.Builder
	for c := range r.Chunks() {
		if t, ok := c.(Text); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String(), r.err
}

func (r *ChatResponse) Thoughts() <-chan string {
	out := make(chan string, 100)
	go func() {
		defer close(out)
		for c := range r.Chunks() {
			if t, ok := c.(Thought); ok {
				out <- t.Text
			}
		}
	}()
	return out
}

func (r *ChatResponse) ToolCalls() <-chan ToolCall {
	out := make(chan ToolCall, 100)
	go func() {
		defer close(out)
		for c := range r.Chunks() {
			// In Go, ToolCall is pushed directly into the chunks channel
			if tc, ok := c.(ToolCall); ok {
				out <- tc
			}
		}
	}()
	return out
}

func (r *ChatResponse) StructuredOutput() (any, error) {
	// Drain the stream
	_, _ = r.Text()
	return r.conv.GetLastStructuredOutput(), nil
}

func (r *ChatResponse) UsageMetadata() *UsageMetadata {
	return r.conv.TurnUsage()
}

func (r *ChatResponse) Cancel(ctx context.Context) error {
	return r.conv.Cancel(ctx)
}

func (tc ToolCall) isStreamChunk() {}
func (tc ToolCall) GetStepIndex() int { return 0 }

// BaseAgentConfig holds the fields shared across different connection strategies.
type BaseAgentConfig struct {
	SystemInstructions  SystemInstructions
	Capabilities        CapabilitiesConfig
	Tools               []any
	Policies            []any
	Hooks               []any
	Triggers            []any
	McpServers          []McpServerConfig
	Workspaces          []string
	ConversationID      *string
	SaveDir             *string
	AppDataDir          *string
	ResponseSchema      *string
	SkillsPaths         []string
}

// GetBaseConfig returns the pointer to BaseAgentConfig.
func (b *BaseAgentConfig) GetBaseConfig() *BaseAgentConfig {
	return b
}

// AgentConfig represents the configuration interface for initializing a strategy.
type AgentConfig interface {
	CreateStrategy(toolRunner any, hookRunner any) (ConnectionStrategy, error)
	GetBaseConfig() *BaseAgentConfig
}

// Connection represents a live session with an agent backend.
type Connection interface {
	IsIdle() bool
	ConversationID() string
	Send(ctx context.Context, prompt Content) error
	ReceiveSteps(ctx context.Context) <-chan *Step
	Disconnect(ctx context.Context) error
	Cancel(ctx context.Context) error
	Delete(ctx context.Context) error
	SignalIdle(ctx context.Context) error
	WaitForIdle(ctx context.Context) error
	WaitForWakeup(ctx context.Context, timeout time.Duration) (bool, error)
	SendToolResults(ctx context.Context, results []ToolResult) error
	SendTriggerNotification(ctx context.Context, content string) error
}

// ConnectionStrategy manages starting the backend, connecting, and clean teardown.
type ConnectionStrategy interface {
	Start(ctx context.Context) error
	Connect() (Connection, error)
	io.Closer
}

// Interfaces to allow root package to interact with subpackages without importing them (breaking cycles).

type HookRunner interface {
	RegisterHook(hook any)
}

type ToolRunner interface {
	SetContext(ctx any)
}

type TriggerRunner interface {
	Start(ctx context.Context) error
	Stop()
}

var DefaultNewHookRunner func() HookRunner
var DefaultNewToolRunner func(tools []any) ToolRunner
var DefaultNewTriggerRunner func(trigs []any, conn any) TriggerRunner
var EnforcePolicies func(policies []any, mcpServers []McpServerConfig) any
var DefaultToolFromFunc func(name string, description string, fn any) (any, error)


