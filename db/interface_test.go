package db

import (
	"io"
	"testing"
)

type Persister interface {
	Get(key string, value any) error
	Put(key string, value any) error
	io.Closer
}

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
