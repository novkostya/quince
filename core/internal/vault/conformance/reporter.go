package conformance

import "fmt"

// T is the subset of *testing.T the suite uses.
//
// AN INTERFACE RATHER THAN *testing.T, so that the suite's OWN CONTROL can run it and
// observe whether it failed. `testing.TB` cannot be implemented outside the standard
// library (it has an unexported method), and a suite hard-wired to *testing.T can only be
// checked by a human reading it. This is what makes RunMutantMustFail possible, and that
// control is the difference between a gate and a formality.
type T interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
}

// recorder is a T that remembers rather than reports. Fatalf and Skipf unwind the check
// they are in — matching *testing.T's semantics, where both abort the current test — and
// the suite runs each check under its own recovery so one abort does not end the run.
type recorder struct {
	failures []string
	skipped  bool
}

type abort struct{ skip bool }

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
	panic(abort{})
}

func (r *recorder) Skipf(format string, args ...any) {
	r.skipped = true
	panic(abort{skip: true})
}

func (r *recorder) failed() bool { return len(r.failures) > 0 }

// guard runs one check, absorbing a Fatalf or Skipf so the rest of the suite continues.
// A panic that is NOT one of ours is re-raised: a nil dereference in an implementation
// under test is a defect to surface, never something for the harness to swallow.
func guard(fn func()) {
	defer func() {
		if p := recover(); p != nil {
			if _, ours := p.(abort); !ours {
				panic(p)
			}
		}
	}()
	fn()
}
