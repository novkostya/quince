package messages

// ScratchDirForTest exposes the scratch directory so a test can assert that nothing was
// written there yet. Test-only, in an _test.go file, so it is not part of the package's API.
func ScratchDirForTest(r *Reader) string { return r.scratch }
