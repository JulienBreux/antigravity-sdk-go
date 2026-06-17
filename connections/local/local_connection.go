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

package local

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JulienBreux/antigravity-sdk-go"
	"github.com/JulienBreux/antigravity-sdk-go/connections/local/pb"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var defaultAppDataDir = filepath.Join(os.Getenv("HOME"), ".gemini", "antigravity")

// LocalAgentConfig configuration for the local harness backend.
type LocalAgentConfig struct {
	antigravity.BaseAgentConfig
	GeminiConfig antigravity.GeminiConfig
	Model        *string
	APIKey       *string
	Vertex       *bool
	Project      *string
	Location     *string
}

func NewLocalAgentConfig() *LocalAgentConfig {
	cwd, _ := os.Getwd()
	cfg := &LocalAgentConfig{
		GeminiConfig: antigravity.GeminiConfig{
			Models: antigravity.NewDefaultModelConfig(),
		},
	}
	cfg.Capabilities = antigravity.CapabilitiesConfig{
		EnableSubagents: true,
	}
	cfg.Workspaces = []string{cwd}
	return cfg
}

func (cfg *LocalAgentConfig) CreateStrategy(toolRunner any, hookRunner any) (antigravity.ConnectionStrategy, error) {
	// Apply shorthand fields to gemini config
	gemini := cfg.GeminiConfig
	if cfg.Model != nil {
		gemini.Models.Default = antigravity.ModelEntry{Name: *cfg.Model}
	}
	if cfg.APIKey != nil {
		gemini.APIKey = cfg.APIKey
	}
	if cfg.Vertex != nil {
		gemini.Vertex = *cfg.Vertex
	}
	if cfg.Project != nil {
		gemini.Project = cfg.Project
	}
	if cfg.Location != nil {
		gemini.Location = cfg.Location
	}

	// Workspaces defaults
	workspaces := cfg.Workspaces
	appDataDir := ""
	if cfg.AppDataDir != nil {
		appDataDir = *cfg.AppDataDir
	} else {
		appDataDir = defaultAppDataDir
	}

	saveDir := ""
	if cfg.SaveDir != nil {
		saveDir = *cfg.SaveDir
	} else {
		var err error
		saveDir, err = os.MkdirTemp("", "antigravity_")
		if err != nil {
			return nil, fmt.Errorf("failed to create save dir: %w", err)
		}
	}

	binaryPath := os.Getenv("ANTIGRAVITY_HARNESS_PATH")
	if binaryPath == "" {
		// Check bin/ relative to the working directory (populated by scripts/download_harness.sh)
		localBin := filepath.Join("bin", "localharness")
		if runtime.GOOS == "windows" {
			localBin = filepath.Join("bin", "localharness.exe")
		}
		if info, err := os.Stat(localBin); err == nil && !info.IsDir() {
			binaryPath = localBin
		}
	}
	if binaryPath == "" {
		// Fallback: check in system PATH
		var err error
		binaryPath, err = exec.LookPath("localharness")
		if err != nil {
			return nil, errors.New("could not find localharness binary. Run 'scripts/download_harness.sh' to download it, set the ANTIGRAVITY_HARNESS_PATH environment variable, or add it to your PATH")
		}
	}

	convID := ""
	if cfg.ConversationID != nil {
		convID = *cfg.ConversationID
	}

	return &LocalConnectionStrategy{
		toolRunner:         toolRunner,
		hookRunner:         hookRunner,
		geminiConfig:       gemini,
		systemInstructions: cfg.SystemInstructions,
		capabilitiesConfig: cfg.Capabilities,
		conversationID:     convID,
		saveDir:            saveDir,
		workspaces:         workspaces,
		appDataDir:         appDataDir,
		skillsPaths:        cfg.SkillsPaths,
		mcpServers:         cfg.McpServers,
		binaryPath:         binaryPath,
	}, nil
}

type LocalConnectionStrategy struct {
	toolRunner         any
	hookRunner         any
	geminiConfig       antigravity.GeminiConfig
	systemInstructions antigravity.SystemInstructions
	capabilitiesConfig antigravity.CapabilitiesConfig
	conversationID     string
	saveDir            string
	workspaces         []string
	appDataDir         string
	skillsPaths        []string
	mcpServers         []antigravity.McpServerConfig
	binaryPath         string

	process *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	conn    *localConnection
}

func (s *LocalConnectionStrategy) Start(ctx context.Context) error {
	// Validate API Key / Vertex configuration
	useVertex := s.geminiConfig.Vertex
	apiKey := ""
	if s.geminiConfig.APIKey != nil {
		apiKey = *s.geminiConfig.APIKey
	} else {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}

	if !useVertex && apiKey == "" {
		return errors.New("a Gemini API key is required. Set it via GeminiConfig(api_key=...) or the GEMINI_API_KEY environment variable")
	}

	if useVertex {
		project := ""
		if s.geminiConfig.Project != nil {
			project = *s.geminiConfig.Project
		}
		location := ""
		if s.geminiConfig.Location != nil {
			location = *s.geminiConfig.Location
		}
		if apiKey == "" && (project == "" || location == "") {
			return errors.New("for Vertex AI, either a GCP project and location, or an API key must be set")
		}
	}

	// Prepare ClientInfo
	goVersion := runtime.Version()
	clientInfo := &pb.ClientInfo{
		Language:        "go",
		Version:         "0.1.3",
		LanguageVersion: goVersion,
	}

	inputConfig := &pb.InputConfig{
		StorageDirectory: s.saveDir,
		ClientInfo:       clientInfo,
	}

	// Execute process
	cmd := exec.Command(s.binaryPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("failed to open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return fmt.Errorf("failed to open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return fmt.Errorf("failed to start localharness process: %w", err)
	}

	s.process = cmd
	s.stdin = stdin
	s.stdout = stdout
	s.stderr = stderr

	// Write InputConfig to stdin
	serialized, err := proto.Marshal(inputConfig)
	if err != nil {
		s.Close()
		return fmt.Errorf("failed to serialize input config: %w", err)
	}

	length := uint32(len(serialized))
	if err := binary.Write(stdin, binary.LittleEndian, length); err != nil {
		s.Close()
		return fmt.Errorf("failed to write input config length: %w", err)
	}
	if _, err := stdin.Write(serialized); err != nil {
		s.Close()
		return fmt.Errorf("failed to write input config data: %w", err)
	}

	// Read OutputConfig from stdout
	var outLength uint32
	if err := binary.Read(stdout, binary.LittleEndian, &outLength); err != nil {
		s.Close()
		return fmt.Errorf("failed to read output config length: %w", err)
	}

	buf := make([]byte, outLength)
	if _, err := io.ReadFull(stdout, buf); err != nil {
		s.Close()
		return fmt.Errorf("failed to read output config data: %w", err)
	}

	outputConfig := &pb.OutputConfig{}
	if err := proto.Unmarshal(buf, outputConfig); err != nil {
		s.Close()
		return fmt.Errorf("failed to unmarshal output config: %w", err)
	}

	wsURL := fmt.Sprintf("ws://localhost:%d/", outputConfig.Port)

	// Background stderr reader
	stderrLines := make([]string, 0, 50)
	var stderrMutex sync.Mutex
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			stderrMutex.Lock()
			if len(stderrLines) >= 50 {
				stderrLines = stderrLines[1:]
			}
			stderrLines = append(stderrLines, line)
			stderrMutex.Unlock()
			log.Printf("harness stderr: %s", line)
		}
	}()

	// Connect to WebSocket with retry
	var ws *websocket.Conn
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	headers := http.Header{}
	headers.Set("x-goog-api-key", outputConfig.ApiKey)

	maxRetries := 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		var resp *http.Response
		ws, resp, err = dialer.DialContext(ctx, wsURL, headers)
		if err == nil {
			if resp != nil {
				resp.Body.Close()
			}
			break
		}
		if attempt == maxRetries-1 {
			s.Close()
			stderrMutex.Lock()
			tail := strings.Join(stderrLines, "\n")
			stderrMutex.Unlock()
			return fmt.Errorf("failed to connect to localharness websocket: %w. Stderr:\n%s", err, tail)
		}
		select {
		case <-ctx.Done():
			s.Close()
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Send InitializeConversationEvent
	harnessConfig := s.buildHarnessConfig()
	initEvent := &pb.InitializeConversationEvent{
		Config: harnessConfig,
	}
	initEventJSON, err := protojson.Marshal(initEvent)
	if err != nil {
		ws.Close()
		s.Close()
		return fmt.Errorf("failed to marshal init event: %w", err)
	}

	if err := ws.WriteMessage(websocket.TextMessage, initEventJSON); err != nil {
		ws.Close()
		s.Close()
		return fmt.Errorf("failed to send init event: %w", err)
	}

	connCtx, cancelFunc := context.WithCancel(context.Background())
	s.conn = &localConnection{
		strategy:                s,
		ws:                      ws,
		isIdle:                  make(chan struct{}),
		stepQueue:               make(chan any, 100),
		activeSubagents:         make(map[string]bool),
		subagentResponses:       make(map[string]string),
		pendingBuiltinToolCalls: make(map[pendingCallKey]pendingCallValue),
		ctx:                     connCtx,
		cancelFunc:              cancelFunc,
	}
	// Initially idle is set
	close(s.conn.isIdle)
	s.conn.isIdleFlag = true

	// Start WebSocket reader loop
	go s.conn.wsReaderLoop(&stderrLines, &stderrMutex)

	return nil
}

func (s *LocalConnectionStrategy) Connect() (antigravity.Connection, error) {
	if s.conn == nil {
		return nil, errors.New("connection not established. Use Start() first")
	}
	return s.conn, nil
}

func (s *LocalConnectionStrategy) Close() error {
	if s.conn != nil {
		s.conn.Disconnect(context.Background())
	}
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.stdout != nil {
		s.stdout.Close()
	}
	if s.stderr != nil {
		s.stderr.Close()
	}
	if s.process != nil && s.process.Process != nil {
		// Wait or Terminate/Kill
		done := make(chan error, 1)
		go func() {
			done <- s.process.Wait()
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			s.process.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				s.process.Process.Kill()
			}
		}
	}
	return nil
}

func (s *LocalConnectionStrategy) buildHarnessConfig() *pb.HarnessConfig {
	var toolProtos []*pb.Tool
	// Reflection to convert Go tools to pb.Tool would go here, but for now we map what we have.
	// We'll populate tools in a separate file or directly.
	// Since toolRunner might contain tools:
	// s.toolRunner to tools

	var systemInstructionsProto *pb.SystemInstructions
	if s.systemInstructions != nil {
		systemInstructionsProto = &pb.SystemInstructions{}
		switch si := s.systemInstructions.(type) {
		case antigravity.CustomSystemInstructions:
			systemInstructionsProto.Type = &pb.SystemInstructions_Custom{
				Custom: &pb.CustomSystemInstructions{
					Part: []*pb.CustomSystemInstructions_Part{
						{Part: &pb.CustomSystemInstructions_Part_Text{Text: si.Text}},
					},
				},
			}
		case antigravity.TemplatedSystemInstructions:
			appended := &pb.AppendedSystemInstructions{
				CustomIdentity: si.Identity,
			}
			for _, sec := range si.Sections {
				appended.AppendedSections = append(appended.AppendedSections, &pb.AppendedSystemInstructions_Section{
					Title:   sec.Title,
					Content: sec.Content,
				})
			}
			systemInstructionsProto.Type = &pb.SystemInstructions_Appended{
				Appended: appended,
			}
		}
	}

	var geminiConfigProto *pb.GeminiConfig
	geminiConfigProto = &pb.GeminiConfig{
		ModelName: s.geminiConfig.Models.Default.Name,
	}
	effectiveAPIKey := s.geminiConfig.APIKey
	if s.geminiConfig.Models.Default.APIKey != nil {
		effectiveAPIKey = s.geminiConfig.Models.Default.APIKey
	}
	if effectiveAPIKey != nil {
		geminiConfigProto.ApiKey = *effectiveAPIKey
	}
	if s.geminiConfig.Models.Default.Generation.ThinkingLevel != nil {
		geminiConfigProto.ThinkingLevel = string(*s.geminiConfig.Models.Default.Generation.ThinkingLevel)
	}
	geminiConfigProto.UseVertex = s.geminiConfig.Vertex
	if s.geminiConfig.Project != nil {
		geminiConfigProto.Project = *s.geminiConfig.Project
	}
	if s.geminiConfig.Location != nil {
		geminiConfigProto.Location = *s.geminiConfig.Location
	}

	workspaceProtos := make([]*pb.Workspace, len(s.workspaces))
	for i, w := range s.workspaces {
		workspaceProtos[i] = &pb.Workspace{
			WorkspaceType: &pb.Workspace_FilesystemWorkspace{
				FilesystemWorkspace: &pb.FilesystemWorkspace{
					Directory: filepath.ToSlash(w),
				},
			},
		}
	}

	cfg := s.capabilitiesConfig
	// Enabled BuiltinTools
	allTools := map[antigravity.BuiltinTools]bool{
		antigravity.BuiltinListDir:       true,
		antigravity.BuiltinSearchDir:     true,
		antigravity.BuiltinFindFile:      true,
		antigravity.BuiltinViewFile:      true,
		antigravity.BuiltinCreateFile:    true,
		antigravity.BuiltinEditFile:      true,
		antigravity.BuiltinRunCommand:    true,
		antigravity.BuiltinAskQuestion:   true,
		antigravity.BuiltinStartSubagent: true,
		antigravity.BuiltinGenerateImage: true,
		antigravity.BuiltinFinish:        true,
	}

	activeTools := make(map[antigravity.BuiltinTools]bool)
	if cfg.EnabledTools != nil {
		for _, t := range cfg.EnabledTools {
			activeTools[t] = true
		}
	} else if cfg.DisabledTools != nil {
		for t := range allTools {
			activeTools[t] = true
		}
		for _, t := range cfg.DisabledTools {
			delete(activeTools, t)
		}
	} else {
		activeTools = allTools
	}

	subagentEnabled := cfg.EnableSubagents && activeTools[antigravity.BuiltinStartSubagent]

	harnessSideTools := &pb.HarnessSideTools{
		Subagents: &pb.SubagentsConfig{Enabled: subagentEnabled},
		Find:      &pb.FindToolConfig{Enabled: activeTools[antigravity.BuiltinFindFile]},
		UserQuestions: &pb.UserQuestionsConfig{Enabled: activeTools[antigravity.BuiltinAskQuestion]},
		RunCommand: &pb.RunCommandToolConfig{Enabled: activeTools[antigravity.BuiltinRunCommand]},
		FileEdit:   &pb.FileEditToolConfig{Enabled: activeTools[antigravity.BuiltinEditFile]},
		ViewFile:   &pb.ViewFileToolConfig{Enabled: activeTools[antigravity.BuiltinViewFile]},
		WriteToFile: &pb.WriteToFileToolConfig{Enabled: activeTools[antigravity.BuiltinCreateFile]},
		GrepSearch:  &pb.GrepSearchToolConfig{Enabled: activeTools[antigravity.BuiltinSearchDir]},
		ListDir:     &pb.ListDirToolConfig{Enabled: activeTools[antigravity.BuiltinListDir]},
		GenerateImage: &pb.GenerateImageToolConfig{
			Enabled:   activeTools[antigravity.BuiltinGenerateImage],
			ModelName: cfg.ImageModel,
		},
	}

	var mcpServerProtos []*pb.McpServerConfig
	for _, mcp := range s.mcpServers {
		mcpProto := &pb.McpServerConfig{
			Name: mcp.GetName(),
		}
		if mcp.GetTimeoutSeconds() != nil {
			mcpProto.TimeoutSeconds = int32(*mcp.GetTimeoutSeconds())
		}
		// Stdio or Http
		switch ms := mcp.(type) {
		case antigravity.McpStdioServer:
			mcpProto.Transport = &pb.McpServerConfig_Stdio{
				Stdio: &pb.McpStdioTransport{
					Command: ms.Command,
					Args:    ms.Args,
					Env:     ms.Env,
				},
			}
			mcpProto.EnabledTools = ms.EnabledTools
			mcpProto.DisabledTools = ms.DisabledTools
		case antigravity.McpStreamableHttpServer:
			mcpProto.Transport = &pb.McpServerConfig_Http{
				Http: &pb.McpHttpTransport{
					Url:     ms.URL,
					Headers: ms.Headers,
				},
			}
			mcpProto.EnabledTools = ms.EnabledTools
			mcpProto.DisabledTools = ms.DisabledTools
		}
		mcpServerProtos = append(mcpServerProtos, mcpProto)
	}

	finishSchema := ""
	if cfg.FinishToolSchemaJSON != nil {
		finishSchema = *cfg.FinishToolSchemaJSON
	}

	compactionThreshold := uint32(0)
	if cfg.CompactionThreshold != nil {
		compactionThreshold = uint32(*cfg.CompactionThreshold)
	}

	return &pb.HarnessConfig{
		Tools:                toolProtos,
		SystemInstructions:   systemInstructionsProto,
		CascadeId:            s.conversationID,
		ModelConfig: &pb.HarnessConfig_GeminiConfig{
			GeminiConfig: geminiConfigProto,
		},
		Workspaces:           workspaceProtos,
		SkillsPaths:          s.skillsPaths,
		HarnessSideTools:     harnessSideTools,
		CompactionThreshold:  compactionThreshold,
		FinishToolSchemaJson: finishSchema,
		AppDataDir:           s.appDataDir,
		McpServers:           mcpServerProtos,
	}
}

type pendingCallKey struct {
	trajectoryID string
	stepIndex    uint32
}

type pendingCallValue struct {
	toolCall         antigravity.ToolCall
	operationContext any
}

type localConnection struct {
	strategy                *LocalConnectionStrategy
	ws                      *websocket.Conn
	isIdle                  chan struct{}
	idleMutex               sync.Mutex
	isIdleFlag              bool
	stepQueue               chan any
	isReceiving             bool
	receivingMutex          sync.Mutex
	cancelled               bool
	cancelledMsg            string
	clientCancelled         bool
	cascadeID               string
	parentIdle              bool
	activeSubagents         map[string]bool
	subagentResponses       map[string]string
	pendingBuiltinToolCalls map[pendingCallKey]pendingCallValue
	pendingMutex            sync.Mutex
	backgroundTasks         sync.WaitGroup
	disconnecting           bool
	ctx                     context.Context
	cancelFunc              context.CancelFunc
}

func (c *localConnection) IsIdle() bool {
	c.idleMutex.Lock()
	defer c.idleMutex.Unlock()
	return c.isIdleFlag
}

func (c *localConnection) setIdle(idle bool) {
	c.idleMutex.Lock()
	defer c.idleMutex.Unlock()
	if c.isIdleFlag == idle {
		return
	}
	c.isIdleFlag = idle
	if idle {
		close(c.isIdle)
	} else {
		c.isIdle = make(chan struct{})
	}
}

func (c *localConnection) ConversationID() string {
	return c.strategy.conversationID
}

func (c *localConnection) Send(ctx context.Context, prompt antigravity.Content) error {
	// If not idle, wait for idle or drain steps
	if !c.IsIdle() {
		// Wait for idle
		if err := c.WaitForIdle(ctx); err != nil {
			return err
		}
	}

	c.setIdle(false)

	event := &pb.InputEvent{}
	if len(prompt) == 1 {
		if txt, ok := prompt[0].(antigravity.StringContent); ok {
			event.Event = &pb.InputEvent_UserInput{
				UserInput: string(txt),
			}
		} else {
			event.Event = &pb.InputEvent_ComplexUserInput{
				ComplexUserInput: c.buildComplexUserInput(prompt),
			}
		}
	} else if len(prompt) > 1 {
		event.Event = &pb.InputEvent_ComplexUserInput{
			ComplexUserInput: c.buildComplexUserInput(prompt),
		}
	} else {
		event.Event = &pb.InputEvent_UserInput{
			UserInput: "",
		}
	}

	serialized, err := protojson.Marshal(event)
	if err != nil {
		return err
	}

	return c.ws.WriteMessage(websocket.TextMessage, serialized)
}

func (c *localConnection) buildComplexUserInput(content antigravity.Content) *pb.UserInput {
	parts := []*pb.UserInput_Part{}
	for _, primitive := range content {
		switch cp := primitive.(type) {
		case antigravity.StringContent:
			parts = append(parts, &pb.UserInput_Part{
				Part: &pb.UserInput_Part_Text{
					Text: string(cp),
				},
			})
		case antigravity.Media:
			parts = append(parts, &pb.UserInput_Part{
				Part: &pb.UserInput_Part_Media{
					Media: &pb.UserInput_Media{
						MimeType:    cp.GetMimeType(),
						Description: cp.GetDescription(),
						Data:        cp.GetData(),
					},
				},
			})
		case antigravity.SlashCommand:
			parts = append(parts, &pb.UserInput_Part{
				Part: &pb.UserInput_Part_SlashCommand{
					SlashCommand: &pb.UserInput_SlashCommand{
						Name: string(cp.Name),
					},
				},
			})
		}
	}
	return &pb.UserInput{Parts: parts}
}

func (c *localConnection) ReceiveSteps(ctx context.Context) <-chan *antigravity.Step {
	c.receivingMutex.Lock()
	defer c.receivingMutex.Unlock()

	outChan := make(chan *antigravity.Step)

	go func() {
		defer close(outChan)
		if c.cancelled {
			outChan <- &antigravity.Step{
				Status: antigravity.StepStatusCanceled,
				Error:  c.cancelledMsg,
				Source: antigravity.StepSourceSystem,
				Type:   antigravity.StepTypeSystemMessage,
			}
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case val, ok := <-c.stepQueue:
				if !ok {
					return
				}
				if val == nil { // Close sentinel
					return
				}
				if err, ok := val.(error); ok {
					log.Printf("ReceiveSteps got error: %v", err)
					return
				}
				if val == "IDLE" { // Idle sentinel
					continue
				}
				step := val.(*antigravity.Step)
				outChan <- step

				// Handle error
				if step.Status == antigravity.StepStatusError && step.Source == antigravity.StepSourceSystem {
					// Simply logging or handling connection error
					log.Printf("System error in step: %s", step.Error)
				}
			}
		}
	}()

	return outChan
}

func (c *localConnection) Disconnect(ctx context.Context) error {
	c.disconnecting = true
	c.cancelFunc()

	// 1. Dispatch Session End Hooks (would go here)
	// 2. Wait for background tasks
	c.backgroundTasks.Wait()

	// 3. Close websocket
	if c.ws != nil {
		c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.ws.Close()
	}

	// Stdin close signals Go harness to terminate
	if c.strategy.stdin != nil {
		c.strategy.stdin.Close()
	}

	return nil
}

func (c *localConnection) Cancel(ctx context.Context) error {
	c.clientCancelled = true
	event := &pb.InputEvent{
		Event: &pb.InputEvent_HaltRequest{
			HaltRequest: true,
		},
	}
	serialized, err := protojson.Marshal(event)
	if err != nil {
		return err
	}
	return c.ws.WriteMessage(websocket.TextMessage, serialized)
}

func (c *localConnection) Delete(ctx context.Context) error {
	return c.Disconnect(ctx)
}

func (c *localConnection) SignalIdle(ctx context.Context) error {
	c.setIdle(true)
	return nil
}

func (c *localConnection) WaitForIdle(ctx context.Context) error {
	c.idleMutex.Lock()
	idleChan := c.isIdle
	c.idleMutex.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-idleChan:
		return nil
	}
}

func (c *localConnection) WaitForWakeup(ctx context.Context, timeout time.Duration) (bool, error) {
	// Not implemented in Python local harness, return false
	return false, nil
}

func (c *localConnection) SendToolResults(ctx context.Context, results []antigravity.ToolResult) error {
	for _, result := range results {
		if result.ID == nil {
			return errors.New("tool result missing ID correlation")
		}
		var outputJSON []byte
		if result.Error != nil {
			outputJSON, _ = json.Marshal(map[string]string{"error": *result.Error})
		} else {
			outputJSON, _ = json.Marshal(result.Result)
		}
		resp := &pb.ToolResponse{
			Id:           *result.ID,
			ResponseJson: string(outputJSON),
		}
		inputEvent := &pb.InputEvent{
			Event: &pb.InputEvent_ToolResponse{
				ToolResponse: resp,
			},
		}
		serialized, err := protojson.Marshal(inputEvent)
		if err != nil {
			return err
		}
		if err := c.ws.WriteMessage(websocket.TextMessage, serialized); err != nil {
			return err
		}
	}
	return nil
}

func (c *localConnection) SendTriggerNotification(ctx context.Context, content string) error {
	event := &pb.InputEvent{
		Event: &pb.InputEvent_AutomatedTrigger{
			AutomatedTrigger: content,
		},
	}
	serialized, err := protojson.Marshal(event)
	if err != nil {
		return err
	}
	return c.ws.WriteMessage(websocket.TextMessage, serialized)
}

func (c *localConnection) wsReaderLoop(stderrLines *[]string, stderrMutex *sync.Mutex) {
	defer func() {
		c.stepQueue <- nil // sentinel
		close(c.stepQueue)
	}()

	for {
		_, rawMsg, err := c.ws.ReadMessage()
		if err != nil {
			if c.disconnecting {
				return
			}
			stderrMutex.Lock()
			tail := strings.Join(*stderrLines, "\n")
			stderrMutex.Unlock()
			c.stepQueue <- fmt.Errorf("websocket closed unexpectedly: %w. Stderr tail:\n%s", err, tail)
			return
		}

		event := &pb.OutputEvent{}
		if err := protojson.Unmarshal(rawMsg, event); err != nil {
			log.Printf("failed to parse output event: %v", err)
			continue
		}

		if event.GetStepUpdate() != nil {
			su := event.GetStepUpdate()

			// Map proto to antigravity.Step
			step := &antigravity.Step{
				ID:        su.TrajectoryId + ":" + strconv.Itoa(int(su.StepIndex)),
				StepIndex: int(su.StepIndex),
				Type:      mapStepType(su.ListDirectory, su.FindFile, su.SearchDirectory, su.ViewFile, su.CreateFile, su.EditFile, su.RunCommand, su.Compaction, su.InvokeSubagent, su.GenerateImage, su.Finish, su.Error, su.McpTool),
				Source:    mapSource(su.Source),
				Target:    mapTarget(su.Target),
				Status:    mapStatus(su.State),
				Content:   su.Text,
				Thinking:  su.Thinking,
				Error:     su.ErrorMessage,
			}

			// Deltas
			step.ContentDelta = su.TextDelta
			step.ThinkingDelta = su.ThinkingDelta

			// Usage metadata
			if event.UsageMetadata != nil {
				um := event.UsageMetadata
				step.UsageMetadata = &antigravity.UsageMetadata{
					PromptTokenCount:        &um.PromptTokenCount,
					CachedContentTokenCount: &um.CachedContentTokenCount,
					CandidatesTokenCount:    &um.CandidatesTokenCount,
					ThoughtsTokenCount:      &um.ThoughtsTokenCount,
					TotalTokenCount:         &um.TotalTokenCount,
				}
			}

			c.stepQueue <- step

			// Record cascade ID
			if su.CascadeId != "" && su.CascadeId == su.TrajectoryId {
				c.cascadeID = su.CascadeId
			}

			// If state is waiting for user
			if su.State == pb.StepUpdate_STATE_WAITING_FOR_USER {
				if su.QuestionsRequest != nil {
					c.backgroundTasks.Add(1)
					go func() {
						defer c.backgroundTasks.Done()
						c.handleQuestionRequest(su)
					}()
				}
				if su.ToolConfirmationRequest != nil {
					c.backgroundTasks.Add(1)
					go func() {
						defer c.backgroundTasks.Done()
						c.handleToolConfirmationRequest(su)
					}()
				}
			}

		} else if event.GetTrajectoryStateUpdate() != nil {
			tsu := event.GetTrajectoryStateUpdate()
			isSubagent := c.cascadeID != "" && tsu.TrajectoryId != c.cascadeID

			if tsu.State == pb.TrajectoryStateUpdate_STATE_RUNNING {
				if isSubagent {
					c.activeSubagents[tsu.TrajectoryId] = true
				}
			} else if tsu.State == pb.TrajectoryStateUpdate_STATE_IDLE {
				if isSubagent {
					delete(c.activeSubagents, tsu.TrajectoryId)
					// post subagent hook dispatch would go here
				} else {
					c.parentIdle = true
				}

				if c.parentIdle && len(c.activeSubagents) == 0 {
					c.setIdle(true)
					c.stepQueue <- "IDLE" // sentinel
				}
			}

		} else if event.GetToolCall() != nil {
			tc := event.GetToolCall()
			c.backgroundTasks.Add(1)
			go func() {
				defer c.backgroundTasks.Done()
				c.handleToolCall(tc)
			}()
		}
	}
}

func (c *localConnection) handleQuestionRequest(su *pb.StepUpdate) {
	// Dispatch interaction hooks or fallback to skipping
	answers := make([]*pb.UserQuestionAnswer, len(su.QuestionsRequest.Questions))
	for i := range su.QuestionsRequest.Questions {
		answers[i] = &pb.UserQuestionAnswer{
			Answer: &pb.UserQuestionAnswer_Unanswered{Unanswered: true},
		}
	}

	resp := &pb.UserQuestionsResponse{
		TrajectoryId: su.TrajectoryId,
		StepIndex:    su.StepIndex,
		Result: &pb.UserQuestionsResponse_Response{
			Response: &pb.UserQuestionsResponse_QuestionsResponse{
				Answers: answers,
			},
		},
	}
	inputEvent := &pb.InputEvent{
		Event: &pb.InputEvent_QuestionResponse{
			QuestionResponse: resp,
		},
	}
	serialized, _ := protojson.Marshal(inputEvent)
	c.ws.WriteMessage(websocket.TextMessage, serialized)
}

func (c *localConnection) handleToolConfirmationRequest(su *pb.StepUpdate) {
	// Automatic approval in Go SDK for local client tools, hooks can deny later
	resp := &pb.ToolConfirmation{
		TrajectoryId: su.TrajectoryId,
		StepIndex:    su.StepIndex,
		Accepted:     true,
	}
	inputEvent := &pb.InputEvent{
		Event: &pb.InputEvent_ToolConfirmation{
			ToolConfirmation: resp,
		},
	}
	serialized, _ := protojson.Marshal(inputEvent)
	c.ws.WriteMessage(websocket.TextMessage, serialized)
}

func (c *localConnection) handleToolCall(tc *pb.ToolCall) {
	// Dispatch tool call to tool runner
	var args map[string]any
	json.Unmarshal([]byte(tc.ArgumentsJson), &args)

	// Since toolRunner will execute, we marshal results and return them
	errStr := "tool runner not configured in Go SDK yet"
	result := antigravity.ToolResult{
		Name:  tc.Name,
		ID:    &tc.Id,
		Error: &errStr,
	}
	c.SendToolResults(c.ctx, []antigravity.ToolResult{result})
}

func mapStepType(listDir *pb.ActionListDirectory, findFile *pb.ActionFindFile, searchDir *pb.ActionSearchDirectory, viewFile *pb.ActionViewFile, createFile *pb.ActionCreateFile, editFile *pb.ActionEditFile, runCmd *pb.ActionRunCommand, compaction *pb.ActionCompaction, subagent *pb.ActionInvokeSubagent, generateImage *pb.ActionGenerateImage, finish *pb.ActionFinish, err *pb.ActionError, mcp *pb.ActionMcpTool) antigravity.StepType {
	if listDir != nil || findFile != nil || searchDir != nil || viewFile != nil || createFile != nil || editFile != nil || runCmd != nil || generateImage != nil || mcp != nil {
		return antigravity.StepTypeToolCall
	}
	if compaction != nil {
		return antigravity.StepTypeCompaction
	}
	if finish != nil {
		return antigravity.StepTypeFinish
	}
	return antigravity.StepTypeTextResponse
}

func mapSource(s pb.StepUpdate_Source) antigravity.StepSource {
	switch s {
	case pb.StepUpdate_SOURCE_SYSTEM:
		return antigravity.StepSourceSystem
	case pb.StepUpdate_SOURCE_USER:
		return antigravity.StepSourceUser
	case pb.StepUpdate_SOURCE_MODEL:
		return antigravity.StepSourceModel
	default:
		return antigravity.StepSourceUnknown
	}
}

func mapTarget(t pb.StepUpdate_Target) antigravity.StepTarget {
	switch t {
	case pb.StepUpdate_TARGET_USER:
		return antigravity.StepTargetUser
	case pb.StepUpdate_TARGET_ENVIRONMENT:
		return antigravity.StepTargetEnvironment
	case pb.StepUpdate_TARGET_UNSPECIFIED:
		return antigravity.StepTargetUnspecified
	default:
		return antigravity.StepTargetUnknown
	}
}

func mapStatus(s pb.StepUpdate_State) antigravity.StepStatus {
	switch s {
	case pb.StepUpdate_STATE_ACTIVE:
		return antigravity.StepStatusActive
	case pb.StepUpdate_STATE_DONE:
		return antigravity.StepStatusDone
	case pb.StepUpdate_STATE_WAITING_FOR_USER:
		return antigravity.StepStatusWaitingForUser
	case pb.StepUpdate_STATE_ERROR:
		return antigravity.StepStatusError
	default:
		return antigravity.StepStatusUnknown
	}
}
