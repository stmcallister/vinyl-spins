package demobig

// Touch is referenced from the app so this package is always compiled.
func Touch() int {
	// These are in generated.go (written by tools/demobiggen).
	return len(LookupStrings) + len(LookupInts)
}

