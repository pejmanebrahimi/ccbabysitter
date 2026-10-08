package main

import (
	"os"
	"testing"
)

// The null device is a character device but no terminal: input from it,
// as a script or an agent gives, is no one to ask a question.
func TestTheNullDeviceIsNoTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatalf("%s counted as a terminal", os.DevNull)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Fatal("a pipe counted as a terminal")
	}
}
