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

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/JulienBreux/antigravity-sdk-go"
)

// ToolContext is the conversation-aware context injected into tools.
type ToolContext struct {
	conn  antigravity.Connection
	state map[string]any
	mu    sync.RWMutex
}

func NewToolContext(conn antigravity.Connection) *ToolContext {
	return &ToolContext{
		conn:  conn,
		state: make(map[string]any),
	}
}

func (c *ToolContext) ConversationID() string {
	return c.conn.ConversationID()
}

func (c *ToolContext) IsIdle() bool {
	return c.conn.IsIdle()
}

func (c *ToolContext) Send(ctx context.Context, message string) error {
	return c.conn.SendTriggerNotification(ctx, message)
}

func (c *ToolContext) GetState(key string, defaultValue any) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.state[key]
	if !ok {
		return defaultValue
	}
	return val
}

func (c *ToolContext) SetState(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state[key] = value
}

// Tool represents a registered capability callable by the agent.
type Tool struct {
	Name        string
	Description string
	Parameters  string // JSON Schema string
	callback    func(ctx context.Context, toolCtx *ToolContext, args map[string]any) (any, error)
}

func init() {
	antigravity.DefaultToolFromFunc = func(name string, description string, fn any) (any, error) {
		return ToolFromFunc(name, description, fn)
	}
}

// ToolFromFunc creates a Tool from a Go function using reflection.
func ToolFromFunc(name string, description string, fn any) (Tool, error) {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()
	if fnType.Kind() != reflect.Func {
		return Tool{}, errors.New("fn must be a function")
	}

	var reqType reflect.Type

	numIn := fnType.NumIn()
	for i := 0; i < numIn; i++ {
		paramType := fnType.In(i)
		if paramType.Kind() == reflect.Struct {
			reqType = paramType
		}
	}

	var parametersJSON string
	if reqType != nil {
		schemaMap := generateJSONSchema(reqType)
		bytes, _ := json.Marshal(schemaMap)
		parametersJSON = string(bytes)
	} else {
		parametersJSON = `{"type":"object"}`
	}

	callback := func(ctx context.Context, toolCtx *ToolContext, args map[string]any) (any, error) {
		inArgs := make([]reflect.Value, numIn)
		for i := 0; i < numIn; i++ {
			paramType := fnType.In(i)
			if paramType.Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
				inArgs[i] = reflect.ValueOf(ctx)
			} else if paramType == reflect.TypeOf((*ToolContext)(nil)) {
				inArgs[i] = reflect.ValueOf(toolCtx)
			} else if paramType.Kind() == reflect.Struct {
				newStruct := reflect.New(paramType).Elem()
				populateStruct(newStruct, args)
				inArgs[i] = newStruct
			}
		}

		results := fnVal.Call(inArgs)
		if len(results) == 0 {
			return nil, nil
		}
		if len(results) == 1 {
			val := results[0].Interface()
			if err, ok := val.(error); ok {
				return nil, err
			}
			return val, nil
		}

		var err error
		errVal := results[len(results)-1].Interface()
		if errVal != nil {
			err = errVal.(error)
		}
		return results[0].Interface(), err
	}

	return Tool{
		Name:        name,
		Description: description,
		Parameters:  parametersJSON,
		callback:    callback,
	}, nil
}

func generateJSONSchema(t reflect.Type) map[string]any {
	properties := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		jsonTag := f.Tag.Get("json")
		name := f.Name
		if jsonTag != "" && jsonTag != "-" {
			parts := strings.Split(jsonTag, ",")
			name = parts[0]
		}
		desc := f.Tag.Get("description")

		prop := map[string]any{
			"type": goTypeToJSONType(f.Type),
		}
		if desc != "" {
			prop["description"] = desc
		}
		properties[name] = prop
		required = append(required, name)
	}

	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

func goTypeToJSONType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	default:
		return "string"
	}
}

func populateStruct(structVal reflect.Value, args map[string]any) {
	t := structVal.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Name
		jsonTag := f.Tag.Get("json")
		if jsonTag != "" && jsonTag != "-" {
			parts := strings.Split(jsonTag, ",")
			name = parts[0]
		}
		val, ok := args[name]
		if !ok {
			continue
		}

		structField := structVal.Field(i)
		if structField.CanSet() {
			valVal := reflect.ValueOf(val)
			if valVal.Type().ConvertibleTo(structField.Type()) {
				structField.Set(valVal.Convert(structField.Type()))
			} else if structField.Kind() == reflect.Int64 {
				if fl, ok := val.(float64); ok {
					structField.SetInt(int64(fl))
				}
			} else if structField.Kind() == reflect.Int {
				if fl, ok := val.(float64); ok {
					structField.SetInt(int64(fl))
				}
			}
		}
	}
}
