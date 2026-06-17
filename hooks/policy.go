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

package hooks

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JulienBreux/antigravity-sdk-go"
)

type Decision string

const (
	DecisionApprove Decision = "APPROVE"
	DecisionDeny    Decision = "DENY"
	DecisionAskUser Decision = "ASK_USER"
)

type Predicate func(ctx context.Context, args map[string]any) (bool, error)
type AskUserHandler func(ctx context.Context, call antigravity.ToolCall) (bool, error)

type Policy struct {
	Tool     string
	Decision Decision
	When     Predicate
	AskUser  AskUserHandler
	Name     string
}

func Allow(tool string, when Predicate, name string) Policy {
	return Policy{Tool: tool, Decision: DecisionApprove, When: when, Name: name}
}

func Deny(tool string, when Predicate, name string) Policy {
	return Policy{Tool: tool, Decision: DecisionDeny, When: when, Name: name}
}

func AskUser(tool string, handler AskUserHandler, when Predicate, name string) Policy {
	return Policy{Tool: tool, Decision: DecisionAskUser, AskUser: handler, When: when, Name: name}
}

func AllowAll() Policy {
	return Allow("*", nil, "allow_all")
}

func DenyAll() Policy {
	return Deny("*", nil, "deny_all")
}

func ConfirmRunCommand(handler AskUserHandler) []Policy {
	if handler != nil {
		return []Policy{
			AskUser(string(antigravity.BuiltinRunCommand), handler, nil, "confirm_run_command"),
			AllowAll(),
		}
	}
	return []Policy{
		Deny(string(antigravity.BuiltinRunCommand), nil, "confirm_run_command"),
		AllowAll(),
	}
}

func WorkspaceOnly(workspaces []string) []Policy {
	outsideWorkspace := func(ctx context.Context, args map[string]any) (bool, error) {
		var path string
		for _, k := range []string{"path", "file_path", "TargetFile", "directory_path"} {
			if v, ok := args[k].(string); ok && v != "" {
				path = v
				break
			}
		}
		if path == "" {
			return false, nil
		}
		normPath, err := filepath.Abs(path)
		if err != nil {
			return true, nil
		}

		for _, ws := range workspaces {
			normWS, err := filepath.Abs(ws)
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(normWS, normPath)
			if err == nil && !strings.HasPrefix(rel, "..") {
				return false, nil
			}
		}
		return true, nil
	}

	var fileTools = []antigravity.BuiltinTools{
		antigravity.BuiltinViewFile,
		antigravity.BuiltinCreateFile,
		antigravity.BuiltinEditFile,
	}

	var policies []Policy
	for _, t := range fileTools {
		policies = append(policies, Deny(string(t), outsideWorkspace, "workspace_only"))
	}
	return policies
}

type EnforcedPolicyHook struct {
	buckets     [][]Policy
	serverNames []string
}

func init() {
	antigravity.EnforcePolicies = func(policies []any, mcpServers []antigravity.McpServerConfig) any {
		var concrete []Policy
		for _, p := range policies {
			if pl, ok := p.(Policy); ok {
				concrete = append(concrete, pl)
			} else if pls, ok := p.([]Policy); ok {
				concrete = append(concrete, pls...)
			}
		}
		return Enforce(concrete, mcpServers)
	}
}

func Enforce(policies []Policy, mcpServers []antigravity.McpServerConfig) PreToolCallDecideHook {
	buckets := make([][]Policy, 9)
	for _, p := range policies {
		idx := bucketIndex(p)
		buckets[idx] = append(buckets[idx], p)
	}
	var serverNames []string
	for _, ms := range mcpServers {
		serverNames = append(serverNames, ms.GetName())
	}
	return &EnforcedPolicyHook{
		buckets:     buckets,
		serverNames: serverNames,
	}
}

func bucketIndex(p Policy) int {
	isGlobal := p.Tool == "*"
	isPrefix := strings.HasSuffix(p.Tool, "/*")

	if isGlobal {
		switch p.Decision {
		case DecisionDeny:
			return 6
		case DecisionAskUser:
			return 7
		default:
			return 8
		}
	}
	if isPrefix {
		switch p.Decision {
		case DecisionDeny:
			return 3
		case DecisionAskUser:
			return 4
		default:
			return 5
		}
	}
	switch p.Decision {
	case DecisionDeny:
		return 0
	case DecisionAskUser:
		return 1
	default:
		return 2
	}
}

func (h *EnforcedPolicyHook) Run(ctx *HookContext, call antigravity.ToolCall) (antigravity.HookResult, error) {
	var callTarget string
	var isMcp bool
	if strings.HasPrefix(call.Name, "mcp_") {
		rest := call.Name[4:]
		for _, server := range h.serverNames {
			if strings.HasPrefix(rest, server+"_") {
				toolName := rest[len(server)+1:]
				callTarget = server + "/" + toolName
				isMcp = true
				break
			}
		}
	}
	if !isMcp {
		callTarget = call.Name
	}

	for _, bucket := range h.buckets {
		for _, p := range bucket {
			if matchesPolicy(p.Tool, callTarget, isMcp) {
				matched := true
				if p.When != nil {
					var err error
					matched, err = p.When(context.Background(), call.Args)
					if err != nil {
						return antigravity.HookResult{Allow: false, Message: fmt.Sprintf("policy predicate error: %v", err)}, nil
					}
				}
				if matched {
					label := p.Name
					if label == "" {
						label = p.Tool
					}
					if p.Decision == DecisionDeny {
						return antigravity.HookResult{Allow: false, Message: fmt.Sprintf("Denied by policy '%s'.", label)}, nil
					}
					if p.Decision == DecisionApprove {
						return antigravity.HookResult{Allow: true}, nil
					}
					// DecisionAskUser
					if p.AskUser != nil {
						approved, err := p.AskUser(context.Background(), call)
						if err != nil {
							return antigravity.HookResult{Allow: false, Message: fmt.Sprintf("AskUser handler error: %v", err)}, nil
						}
						return antigravity.HookResult{Allow: approved}, nil
					}
					return antigravity.HookResult{Allow: false, Message: "No AskUser handler configured"}, nil
				}
			}
		}
	}
	return antigravity.HookResult{Allow: true}, nil
}

func matchesPolicy(policyTool, callTarget string, isMcp bool) bool {
	if policyTool == "*" {
		return true
	}
	if isMcp {
		if strings.HasSuffix(policyTool, "/*") {
			policyServer := strings.TrimSuffix(policyTool, "/*")
			callServer := strings.SplitN(callTarget, "/", 2)[0]
			return policyServer == callServer
		}
		return policyTool == callTarget
	}
	return policyTool == callTarget
}
