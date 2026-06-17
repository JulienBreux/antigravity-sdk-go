# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

.PHONY: setup test build examples clean

## setup: Download the localharness binary and prepare the project
setup: bin/localharness

bin/localharness:
	@./scripts/download_harness.sh

## test: Run all tests
test:
	go test -v ./...

## build: Build all packages
build:
	go build ./...

## examples: Verify all examples compile
examples:
	go build -o /dev/null ./examples/hello_world/...
	go build -o /dev/null ./examples/custom_tools/...
	go build -o /dev/null ./examples/triggers_and_policies/...

## clean: Remove downloaded binaries
clean:
	rm -rf bin/

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'
