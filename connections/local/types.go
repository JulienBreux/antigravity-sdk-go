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
	"fmt"
	"strings"
)

// RunCommandResult is the structured result from a run_command tool execution.
type RunCommandResult struct {
	Output string
}

func (r RunCommandResult) String() string {
	return r.Output
}

// ListDirectoryEntry is a single entry in a directory listing.
type ListDirectoryEntry struct {
	Name        string
	IsDirectory bool
	FileSize    uint64
}

// ListDirectoryResult is the structured result from a list_directory tool execution.
type ListDirectoryResult struct {
	Entries []ListDirectoryEntry
}

func (r ListDirectoryResult) String() string {
	var parts []string
	for _, e := range r.Entries {
		if e.IsDirectory {
			parts = append(parts, fmt.Sprintf("%s/ (dir)", e.Name))
		} else {
			parts = append(parts, fmt.Sprintf("%s (%d bytes)", e.Name, e.FileSize))
		}
	}
	return strings.Join(parts, "\n")
}

// SearchDirectoryResult is the structured result from a search_directory tool execution.
type SearchDirectoryResult struct {
	NumResults int32
}

func (r SearchDirectoryResult) String() string {
	return fmt.Sprintf("%d results", r.NumResults)
}

// FindFileResult is the structured result from a find_file tool execution.
type FindFileResult struct {
	Output string
}

func (r FindFileResult) String() string {
	return r.Output
}

// EditFileResult is the structured result from an edit_file tool execution.
type EditFileResult struct {
	Summary string
}

func (r EditFileResult) String() string {
	return r.Summary
}

// GenerateImageResult is the structured result from a generate_image tool execution.
type GenerateImageResult struct {
	ImageName string
}

func (r GenerateImageResult) String() string {
	return r.ImageName
}

// TextResult is the fallback for tools without structured output.
type TextResult struct {
	Text string
}

func (r TextResult) String() string {
	return r.Text
}
