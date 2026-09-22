// Command canonicalize prints the canonical rendering of a SimpleFIN fixture
// using the generated Go SDK.
//
// It exists so the cross-language check can compare the Go and Node SDKs'
// output directly, rather than only comparing each against the expectation
// files. Two implementations can both match a fixture's expectation and still
// disagree on a fixture nobody wrote an expectation for.
package main

import (
	"fmt"
	"os"

	simplefin "github.com/andrepato/simplefin-sdk/sdk/go"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: canonicalize <fixture.json>")
		os.Exit(2)
	}

	input, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "canonicalize:", err)
		os.Exit(1)
	}

	set, err := simplefin.ParseAccountSet(input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "canonicalize:", err)
		os.Exit(1)
	}

	out, err := simplefin.CanonicalJSON(set)
	if err != nil {
		fmt.Fprintln(os.Stderr, "canonicalize:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
