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

// Package resource provides facilities for tracking resource lifetimes.
//
// In Go, it is common for resources to be allocated by the NewXXX function
// and released by the Close or similar method.
// This package allows tracking of such resources with [custom pprof profiles]
// and ensures that this method is actually called.
//
// To use it, first create a [Handle] with the [NewHandle] function
// and store it in a resource being tracked, typically as a non-embedded pointer field of a struct.
// Then, call [Track] with both the resource and handler pointer.
// It is recommended to do all of that in the NewXXX function.
//
// Resource's Close method implementation should call [Untrack].
// If the resource becomes unreachable and is garbage-collected without this method being called,
// the runtime would panic with a stack trace showing the Track call.
//
// Additionally, currently traced resources are shown in custom pprof profiles named after resource types.
//
// [custom pprof profiles]: https://tip.golang.org/wiki/CustomPprofProfiles
package resource

import (
	"reflect"
	"runtime"
	"runtime/pprof"
	"sync"
)

const (
	collectStack = true
	pprofEnabled = true
	pprofPrefix  = "resource/"
)

// cleanup is called by the runtime when the [Track]ed resource is no longer reachable,
// but [Untrack] wasn't called on it.
// It panics with the given message.
//
// This variable is overridden in tests.
var cleanup = func(h *Handle) {
	msg := h.buildPanicMsg()
	panic(msg)
}

// pprofM protects access to pprof profiles.
var pprofM sync.Mutex

// Track tracks the lifetime of an resource until [Untrack] is called on it.
func Track[T any](resource *T, h *Handle) {
	if resource == nil {
		panic("resource must not be nil")
	}

	if h == nil {
		panic("handle must not be nil")
	}

	if pprofEnabled {
		profile := profileName(resource)

		// fast path

		p := pprof.Lookup(profile)

		if p == nil {
			// slow path

			pprofM.Lock()

			// a concurrent call might have created a profile already
			if p = pprof.Lookup(profile); p == nil {
				p = pprof.NewProfile(profile)
			}

			pprofM.Unlock()
		}

		// Use handle instead of resource itself,
		// because otherwise profile will hold a reference to resource and cleanup will never run.
		p.Add(h, 2)

		h.profile = profile
	}

	h.typ = reflect.TypeOf(resource).String()

	if collectStack {
		// It would be nice to access pprof.Profile's PCs.
		// Unfortunately, the only way to get them is through p.WriteTo,
		// and parsing text or protobuf would be overkill.
		stk := make([]uintptr, 32)
		n := runtime.Callers(2, stk[:])
		h.pcs = stk[:n]
	}

	c := runtime.AddCleanup(resource, cleanup, h)
	h.c.Store(&c)
}

// Untrack stops tracking the lifetime of an resource.
//
// It is safe to call this function multiple times concurrently.
func Untrack[T any](resource *T, h *Handle) {
	if resource == nil {
		panic("resource must not be nil")
	}

	if h == nil {
		panic("handle must not be nil")
	}

	if h := h.c.Swap(nil); h != nil {
		h.Stop()
	}

	// ensure that resource is still reachable before we have a chance to cancel the cleanup call
	runtime.KeepAlive(resource)

	if pprofEnabled {
		p := pprof.Lookup(h.profile)
		if p == nil {
			panic("resource is not tracked")
		}

		p.Remove(h)
	}
}

// profileName return pprof profile name for the given pointer.
func profileName(resource any) string {
	return pprofPrefix + reflect.TypeOf(resource).Elem().String()
}
