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
	"reflect"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
)

var origCleanup = cleanup

// assertEqual fails the test if expected and actual are not equal.
func assertEqual[T any](t testing.TB, expected, actual T) {
	t.Helper()

	if reflect.DeepEqual(expected, actual) {
		return
	}

	t.Errorf("Not equal, but should be:\nexpected: %v\nactual  : %v", expected, actual)
}

// Resource represents a tracked resource for tests.
type Resource struct {
	h *Handle
}

// globalResource is a global resource that is never cleaned up.
var globalResource *Resource

// See https://go.dev/doc/gc-guide#Testing_object_death
// and https://pkg.go.dev/cmd/compile#hdr-Line_Directives.
func TestTrackUntrack(t *testing.T) {
	cleanup = origCleanup

	profile := "resource/resource.Resource"

	runtime.GC()

	t.Run("UntrackWithoutTrack", func(t *testing.T) {
		res := &Resource{h: NewHandle()}
		Untrack(res, res.h)
	})

	t.Run("LocalUntrack", func(t *testing.T) {
		res := &Resource{h: NewHandle()}

//line testtrack.go:100
		Track(res, res.h)

		assertEqual(t, 1, pprof.Lookup(profile).Count())

		Untrack(res, res.h)

		runtime.GC()

		assertEqual(t, 0, pprof.Lookup(profile).Count())
	})

	t.Run("LocalCleanup", func(t *testing.T) {
		h := NewHandle()
		ch := make(chan string, 1)

		t.Cleanup(func() { cleanup = origCleanup })
		cleanup = func(h *Handle) {
			ch <- h.buildPanicMsg()
		}

		res := &Resource{h: h}

//line testtrack.go:200
		Track(res, res.h)

		assertEqual(t, 1, pprof.Lookup(profile).Count())

		runtime.GC()
		msg := <-ch

		assertEqual(t, true, strings.Contains(msg, "testtrack.go:200"))
		assertEqual(t, 1, pprof.Lookup(profile).Count())

		// remove profile manually to support `go test -count=X`
		pprof.Lookup(profile).Remove(h)
		assertEqual(t, 0, pprof.Lookup(profile).Count())
	})

	t.Run("Global", func(t *testing.T) {
		globalResource = &Resource{h: NewHandle()}

//line testtrack.go:300
		Track(globalResource, globalResource.h)

		assertEqual(t, 1, pprof.Lookup(profile).Count())

		runtime.GC()

		assertEqual(t, 1, pprof.Lookup(profile).Count())

		Untrack(globalResource, globalResource.h)

		runtime.GC()

		assertEqual(t, 0, pprof.Lookup(profile).Count())
	})

	runtime.GC()
}

func TestUntrackConcurrently(t *testing.T) {
	cleanup = origCleanup

	res := &Resource{h: NewHandle()}
	Track(res, res.h)

	profile := "resource/resource.Resource"
	assertEqual(t, 1, pprof.Lookup(profile).Count())

	// do a bit more work to reduce a chance that one goroutine would finish
	// before the other one is still being created
	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(-1) * 10
	ready := make(chan struct{}, n)
	start := make(chan struct{})

	for range n {
		wg.Add(1)

		go func() {
			ready <- struct{}{}

			<-start

			Untrack(res, res.h)

			wg.Done()
		}()
	}

	for range n {
		<-ready
	}

	close(start)

	wg.Wait()

	assertEqual(t, 0, pprof.Lookup(profile).Count())

	runtime.GC()
}

func TestStacks(t *testing.T) {
	cleanup = origCleanup

	profile := "resource/resource.Resource"

	h := NewHandle()
	ch := make(chan string, 1)

	t.Cleanup(func() {
		cleanup = origCleanup

		// remove profile manually to support `go test -count=X`
		pprof.Lookup(profile).Remove(h)
		assertEqual(t, 0, pprof.Lookup(profile).Count())
	})
	cleanup = func(h *Handle) {
		ch <- h.buildPanicMsg()
	}

	res := &Resource{h: h}

//line testtrack.go:400
	Track(res, res.h)

	runtime.GC()
	msg := <-ch
	t.Logf("stack:\n%s", msg)

	// resource.Resource became unreachable without being released!
	// It started being tracked at:
	// github.com/AlekSi/resource.TestStacks
	// 	testtrack.go:400
	// testing.tRunner
	// 	/opt/homebrew/Cellar/go/1.25.7_1/libexec/src/testing/testing.go:1934
	// runtime.goexit
	// 	/opt/homebrew/Cellar/go/1.25.7_1/libexec/src/runtime/asm_arm64.s:1268
	expected := []*regexp.Regexp{
		0: regexp.MustCompile(`^\Qresource.Resource became unreachable without being released!\E$`),
		1: regexp.MustCompile(`^\QIt started being tracked at:\E$`),
		2: regexp.MustCompile(`^\Qgithub.com/AlekSi/resource.TestStacks\E$`),
		3: regexp.MustCompile(`^\ttesttrack\.go:400$`),
		4: regexp.MustCompile(`^\Qtesting.tRunner\E$`),
		5: regexp.MustCompile(`^\t[0-9A-Za-z_:/.]+/testing/testing\.go:\d+$`),
		6: regexp.MustCompile(`^\Qruntime.goexit\E$`),
		7: regexp.MustCompile(`^\t[0-9A-Za-z_:/.]+/runtime/\w+\.s:\d+$`),
		8: regexp.MustCompile(`^$`),
	}

	lines := strings.Split(msg, "\n")
	if len(expected) != len(lines) {
		t.Errorf("expected %d lines, got %d", len(expected), len(lines))
	}
	for i, re := range expected {
		if len(lines) == i {
			break
		}

		if l := lines[i]; !re.MatchString(l) {
			t.Errorf("line %d %q expected to match %q", i, l, re.String())
		}
	}

	assertEqual(t, 1, pprof.Lookup(profile).Count())

	var buf strings.Builder
	assertEqual(t, nil, pprof.Lookup(profile).WriteTo(&buf, 1))
	msg = buf.String()
	t.Logf("pprof stack:\n%s", msg)

	lines = strings.Split(msg, "\n")
	assertEqual(t, 6, len(lines))

	// resource/resource.Resource profile: total 1
	// 1 @ 0x104cdaf98 0x104c9f0d8 0x104c41d44
	// #       0x104cdaf97     github.com/AlekSi/resource.TestStacks+0x177     testtrack.go:400
	// #       0x104c9f0d7     testing.tRunner+0xc7                            /<goroot>/testing/testing.go:1934
	assertEqual(t, "resource/resource.Resource profile: total 1", lines[0])
	assertEqual(t, true, strings.Contains(lines[1], " @ 0x"))
	assertEqual(t, true, strings.Contains(lines[2], "github.com/AlekSi/resource.TestStacks"))
	assertEqual(t, true, strings.Contains(lines[2], "testtrack.go:400"))
}

func Example() {
	// The default cleanup function panics with a stack trace of the Track call.
	cleanup = func(h *Handle) {
		fmt.Printf("%s wasn't released!", h.typ)
	}

	res := &Resource{
		h: NewHandle(),
	}

	Track(res, res.h)

	runtime.GC()
	runtime.GC()

	// Output:
	// resource.Resource wasn't released!
}
