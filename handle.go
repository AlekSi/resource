// Copyright 2021 FerretDB Inc.
// Copyright 2026 Alexey Palazhchenko.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resource

import (
	"fmt"
	"runtime"
	"sync/atomic"
)

// Handle holds the [runtime.Cleanup] to stop resource lifetime tracking.
//
// It must be created with [NewHandle].
// It must be passed to [Track] and [Untrack] together with the resource.
// Just creating and storing a handle does not enable tracking.
//
// It must not be used for tracking multiple resources.
//
// It is recommended to store it as non-embedded pointer field of a resource struct being tracked.
type Handle struct {
	c       atomic.Pointer[runtime.Cleanup]
	typ     string
	profile string
	pcs     []uintptr
}

// NewHandle creates a new [Handle].
func NewHandle() *Handle {
	return new(Handle)
}

// buildPanicMsg builds a panic message.
func (h *Handle) buildPanicMsg() string {
	msg := h.typ + " became unreachable without being released!"
	if h.pcs != nil {
		msg += "\nIt started being tracked at:\n"

		frames := runtime.CallersFrames(h.pcs)
		for {
			frame, more := frames.Next()
			msg += fmt.Sprintf("%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line)

			if !more {
				break
			}
		}
	}

	return msg
}
