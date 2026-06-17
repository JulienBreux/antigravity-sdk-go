# Google Antigravity SDK for Go — Examples

Runnable examples demonstrating the usage of the Google Antigravity SDK in Go, organized from basic quickstarts to advanced, stateful tool and policy integration patterns.

## Prerequisites

Before running the examples, ensure you have Go 1.21+ installed and your Gemini API Key exported to your environment:

```bash
export GEMINI_API_KEY="your_gemini_api_key_here"
```

The examples connect to a local harness (`localharness`) automatically. Ensure it is accessible in your `PATH` or specify its path using the `ANTIGRAVITY_HARNESS_PATH` environment variable.

---

## Directory Layout

### 1. [`hello_world/`](hello_world/)
A basic quickstart demonstrating the simplest way to interact with an agent session:
- Initializing a `LocalAgentConfig`
- Starting the agent session
- Sending a simple prompt using `agent.Chat`
- Streaming the response tokens in real-time as they arrive

**To run:**
```bash
go run examples/hello_world/main.go
```

---

### 2. [`custom_tools/`](custom_tools/)
Demonstrates custom tools and stateful conversation-aware tools:
- Defining custom parameter structures with JSON tags and descriptions used for automatic schema reflection.
- Implementing a simple tool function (`lookupFruitSku`) which maps parameters directly.
- Implementing a stateful tool function (`recordFruit`) which accesses `tools.ToolContext` to read and write context state across conversational turns.
- Hook-based safety policies restricting allowed tools (`hooks.DenyAll` and `hooks.Allow`).

**To run:**
```bash
go run examples/custom_tools/main.go
```

---

### 3. [`triggers_and_policies/`](triggers_and_policies/)
Demonstrates a production SRE operations scenario combining background triggers and safety policies:
- Exposing write capabilities (`antigravity.BuiltinRunCommand`) to the agent.
- Enforcing safety policies (`hooks.AskUser` and `hooks.AllowAll`) which intercept and prompt the user for interactive terminal execution approval.
- Registering a background watchdog trigger (`triggers.Every`) which polls an external SRE queue and automatically pushes ticket alerts into the agent session.

**To run:**
```bash
go run examples/triggers_and_policies/main.go
```

---

## Verifying Examples

You can build and test compiling all examples by running:

```bash
go build -o /dev/null ./examples/hello_world/main.go && \
go build -o /dev/null ./examples/custom_tools/main.go && \
go build -o /dev/null ./examples/triggers_and_policies/main.go
```
