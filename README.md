# Google Antigravity SDK for Go

The Google Antigravity SDK is a Go library for building autonomous AI agents powered by Antigravity and Gemini. It provides a secure, scalable, and stateful infrastructure layer that abstracts the agentic loop, letting you focus on what your agent *does* rather than how it runs.

## Prerequisites

- **Go 1.21+**
- **Gemini API Key** — set via `GEMINI_API_KEY` environment variable
- **`localharness` runtime binary** — see [Setup](#setup) below

## Installation

```sh
go get github.com/JulienBreux/antigravity-sdk-go
```

## Setup

The SDK requires the `localharness` runtime binary, which is the Antigravity agent backend. A download script is included to fetch it automatically from PyPI — **no Python installation required**.

### Quick Setup (recommended)

```sh
# Download localharness and prepare the project
make setup
```

### Manual Setup

```sh
# Download the binary for your platform (macOS, Linux, Windows)
./scripts/download_harness.sh

# The binary is placed in bin/localharness
# The SDK automatically finds it there at runtime
```

### Binary Resolution Order

The SDK searches for `localharness` in the following order:

1. **`ANTIGRAVITY_HARNESS_PATH`** environment variable (explicit override)
2. **`bin/localharness`** in the working directory (populated by `make setup`)
3. **`localharness`** in the system `PATH`

> [!TIP]
> For CI/CD pipelines, set `ANTIGRAVITY_HARNESS_PATH` to an absolute path. For local development, `make setup` is the easiest option.

---

## Quickstart

### Simple Agent

The `Agent` struct is the easiest way to get started. It manages the full lifecycle — starting the local harness, registering tools, hooks, safety policies, and background triggers — behind a simple lifecycle pattern.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/JulienBreux/antigravity-sdk-go"
	"github.com/JulienBreux/antigravity-sdk-go/agent"
	"github.com/JulienBreux/antigravity-sdk-go/connections/local"
	_ "github.com/JulienBreux/antigravity-sdk-go/conversation" // Registers conversation factory
	"github.com/JulienBreux/antigravity-sdk-go/hooks"
)

func main() {
	ctx := context.Background()

	// 1. Initialize local configuration
	config := local.NewLocalAgentConfig()

	// Optional: Configure API Key or Model
	apiKey := os.Getenv("GEMINI_API_KEY")
	config.APIKey = &apiKey

	// Safety policy: allow all tool calls for this simple demo
	config.Policies = []any{hooks.AllowAll()}

	// 2. Create the Agent
	myAgent := agent.NewAgent(config)

	// 3. Start the agent session
	if err := myAgent.Start(ctx); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}
	defer myAgent.Close()

	// 4. Send a prompt to the agent
	prompt := antigravity.Content{
		antigravity.StringContent("Say 'Hello World!'"),
	}
	response, err := myAgent.Chat(ctx, prompt)
	if err != nil {
		log.Fatalf("Chat error: %v", err)
	}

	// 5. Get the full response text
	text, err := response.Text()
	if err != nil {
		log.Fatalf("Failed to retrieve text response: %v", err)
	}

	fmt.Printf("Agent Response:\n%s\n", text)
}
```

**Run it:**

```sh
export GEMINI_API_KEY="your_key_here"
make setup        # Download localharness (first time only)
go run ./examples/hello_world/
```

---

## Key Concepts

### Streaming Responses

For fluid console outputs or user interfaces, you can stream the agent's response, thoughts, or tool calls in real-time without waiting for the full turn to finish.

```go
// Stream text tokens as they arrive
for chunk := range response.Chunks() {
	if txt, ok := chunk.(antigravity.Text); ok {
		fmt.Print(txt.Text)
	}
}
fmt.Println()
```

For advanced streaming:
- `response.Thoughts()`: Returns a read channel (`<-chan string`) for internal reasoning steps.
- `response.ToolCalls()`: Returns a read channel (`<-chan ToolCall`) for intercepted tool dispatches.

---

### Multimodal Inputs

Pass text instructions along with images, documents, audio, or video files to the agent.

```go
// Load a PDF document from the filesystem
specDoc, err := antigravity.FromFile("architecture_spec.pdf", "System Architecture Spec")
if err != nil {
	log.Fatal(err)
}

// Or construct in-memory media directly (e.g. for PNG image bytes)
chartImage := antigravity.Image{
	BaseMedia: antigravity.BaseMedia{
		Data:        pngBytes,
		MimeType:    "image/png",
		Description: "System Diagram",
	},
}

prompt := antigravity.Content{
	antigravity.StringContent("Compare this diagram against the spec document and list three security issues:"),
	chartImage,
	specDoc,
}

response, err := agent.Chat(ctx, prompt)
```

---

### Custom Tools

Register Go functions as tools. The SDK inspects function signatures using reflection, automatically generates the JSON Schema for the model, and parses/populates input arguments when called.

```go
type WeatherArgs struct {
	City string `json:"city" description:"The city to get weather for"`
}

// Custom function tool
func getWeather(ctx context.Context, args WeatherArgs) (string, error) {
	return fmt.Sprintf("It is currently sunny and 22°C in %s.", args.City), nil
}

import "github.com/JulienBreux/antigravity-sdk-go/agent"

func main() {
	config := local.NewLocalAgentConfig()
	
	// Register custom tool functions in the Tools slice
	config.Tools = []any{getWeather}
	
	myAgent := agent.NewAgent(config)
	// ... start and chat ...
}
```

If a custom tool function declares a parameter of type `*tools.ToolContext`, it receives the context automatically, allowing it to send background notifications or check the conversation state.

---

### Declarative Safety Policies

By default, the SDK enables all builtin tools. If write capabilities or external MCP servers are used, you **must** provide safety policies to control tool execution.

Policies are evaluated using a strict priority-based matching model.

```go
import (
	"github.com/JulienBreux/antigravity-sdk-go/hooks"
)

func main() {
	config := local.NewLocalAgentConfig()
	config.Capabilities.EnabledTools = []antigravity.BuiltinTools{
		antigravity.BuiltinViewFile,
		antigravity.BuiltinRunCommand,
	}

	// Expose write tools but require user confirmation before executing commands
	config.Policies = []any{
		hooks.AskUser(string(antigravity.BuiltinRunCommand), myApprovalHandler, nil, "confirm_run_cmd"),
		hooks.AllowAll(),
	}

	myAgent := agent.NewAgent(config)
	// ...
}

func myApprovalHandler(ctx context.Context, call antigravity.ToolCall) (bool, error) {
	fmt.Printf("[PROPOSAL] Execute: %v\n", call.Args["CommandLine"])
	fmt.Print("Approve? (y/n): ")
	var input string
	fmt.Scanln(&input)
	return input == "y" || input == "yes", nil
}
```

Built-in policies:
- `hooks.AllowAll()`: Approves all tool calls immediately (useful for local development).
- `hooks.DenyAll()`: Denies all tool calls.
- `hooks.WorkspaceOnly(workspaces)`: Blocks file-modifying tools from reading/writing files outside the configured workspace paths.
- `hooks.ConfirmRunCommand(handler)`: Denies or asks the user before executing shell commands.

---

### Background Triggers

Triggers are concurrent background watchdogs that react to events (timers, files, webhooks) and push messages back to the agent session.

> [!IMPORTANT]
> The `localharness` requires at least one chat turn to initialize the conversation before triggers can send notifications. Always complete an initial chat before enabling trigger logic.

```go
import (
	"github.com/JulienBreux/antigravity-sdk-go/triggers"
)

func main() {
	config := local.NewLocalAgentConfig()

	// Trigger callback runs every 60 seconds
	onTicker := triggers.Every(60*time.Second, func(ctx context.Context, tc *triggers.TriggerContext) error {
		return tc.Send(ctx, "Check the deployment build status.")
	})

	config.Triggers = []any{onTicker}

	myAgent := agent.NewAgent(config)
	// ...
}
```

Available triggers:
- `triggers.Every(interval, callback)`: Periodically invokes a callback.
- `triggers.OnFileChange(path, callback)`: Monitors file changes at the given path (uses file notify system calls).

---

## Examples

Complete working examples are available in the [`examples/`](examples/) directory:

| Example | Description |
|:--------|:------------|
| [`hello_world`](examples/hello_world/) | Minimal agent that sends a prompt and prints the response |
| [`custom_tools`](examples/custom_tools/) | Register Go functions as agent-callable tools |
| [`triggers_and_policies`](examples/triggers_and_policies/) | Background SRE triggers with interactive safety policy approval |

```sh
# Run any example
export GEMINI_API_KEY="your_key_here"
make setup  # first time only
go run ./examples/hello_world/
```

See the [examples README](examples/README.md) for detailed documentation.

---

## Development

```sh
# Download localharness binary
make setup

# Run all tests
make test

# Build all packages
make build

# Verify all examples compile
make examples

# Clean downloaded binaries
make clean

# Show all available targets
make help
```

---

## Architecture

The SDK is organized in a decoupled three-layer architecture to maximize customizability and prevent cyclic imports:

| Layer | Purpose | Key Components |
|:------|:--------|:--------------|
| **Layer 1** — Interface | High-level developer API | `Agent` |
| **Layer 2** — Session | Stateful history and event streaming | `Conversation`, `ChatResponse`, `Step`, `ToolRunner`, `HookRunner`, `TriggerRunner` |
| **Layer 3** — Transport | Process lifecycle, handshakes, and protocol framing | `Connection`, `ConnectionStrategy`, `LocalConnectionStrategy` |

---

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
