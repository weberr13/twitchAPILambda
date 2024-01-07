package db

import (
	"testing"
)

func TestKV(t *testing.T) {
	var p Persister
	var err error

	p, err = NewBadger("")
	if err != nil {
		t.Fatalf("could not get a KV")
	}
	if p.Close() != nil {
		t.Fatal("could not close KV")
	}
}
