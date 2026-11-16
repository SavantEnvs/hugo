// Package fuzzing holds the in-process fuzz entry points for Hugo. Every
// function takes the raw fuzzer bytes, drives one Hugo entry point that reads
// untrusted data, and returns 0 (input used) or -1 (input rejected by Hugo).
package fuzzing

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/gohugoio/hugo/deps"
	"github.com/gohugoio/hugo/hugolib"
	tplstrings "github.com/gohugoio/hugo/tpl/strings"
	"github.com/gohugoio/hugo/tpl/transform"
)

// fuzzTB satisfies testing.TB (via embedding) outside a `go test` process so
// hugolib.NewIntegrationTestBuilder can run inside the fuzz binary. Failure
// methods panic so a broken site build aborts loudly instead of being ignored.
type fuzzTB struct{ testing.TB }

func (fuzzTB) Cleanup(func())            {}
func (fuzzTB) Error(args ...any)         { panic(fmt.Sprint(args...)) }
func (fuzzTB) Errorf(f string, a ...any) { panic(fmt.Sprintf(f, a...)) }
func (fuzzTB) Fail()                     { panic("fuzzTB.Fail") }
func (fuzzTB) FailNow()                  { panic("fuzzTB.FailNow") }
func (fuzzTB) Failed() bool              { return false }
func (fuzzTB) Fatal(args ...any)         { panic(fmt.Sprint(args...)) }
func (fuzzTB) Fatalf(f string, a ...any) { panic(fmt.Sprintf(f, a...)) }
func (fuzzTB) Helper()                   {}
func (fuzzTB) Log(args ...any)           { fmt.Fprintln(os.Stderr, args...) }
func (fuzzTB) Logf(f string, a ...any)   { fmt.Fprintf(os.Stderr, f+"\n", a...) }
func (fuzzTB) Name() string              { return "mayhem-fuzz" }
func (fuzzTB) Setenv(key, value string)  { os.Setenv(key, value) }
func (fuzzTB) Skip(args ...any)          {}
func (fuzzTB) SkipNow()                  {}
func (fuzzTB) Skipf(f string, a ...any)  {}
func (fuzzTB) Skipped() bool             { return false }
func (fuzzTB) TempDir() string {
	d, err := os.MkdirTemp("", "mayhem-fuzz")
	if err != nil {
		panic(err)
	}
	return d
}

var (
	nsOnce sync.Once
	ns     *transform.Namespace
	siteD  *deps.Deps

	stringsOnce sync.Once
	strNS       *tplstrings.Namespace
)

// namespace builds one empty Hugo site and returns its transform template
// function namespace; SiteDeps holds the site's dependencies. The site is only read afterwards, never changed.
func namespace() *transform.Namespace {
	nsOnce.Do(func() {
		workDir, err := os.MkdirTemp("", "mayhem-fuzz-site")
		if err != nil {
			panic(err)
		}
		b := hugolib.NewIntegrationTestBuilder(
			hugolib.IntegrationTestConfig{T: fuzzTB{}, WorkingDir: workDir, NeedsOsFS: true},
		).Build()
		siteD = b.H.Deps
		ns = transform.New(siteD)
	})
	return ns
}

// SiteDeps returns the dependencies of the shared site (read-only; used by mayhem/kat).
func SiteDeps() *deps.Deps {
	namespace()
	return siteD
}

// stringsNamespace returns the strings template function namespace of the shared site.
func stringsNamespace() *tplstrings.Namespace {
	stringsOnce.Do(func() { strNS = tplstrings.New(SiteDeps()) })
	return strNS
}

// Namespace returns the shared transform namespace (used by mayhem/kat).
func Namespace() *transform.Namespace { return namespace() }
