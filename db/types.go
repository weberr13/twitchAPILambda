package db

import (
	"errors"
	"io"
)

// ErrNotFound in the db
var ErrNotFound = errors.New("value not found")

// Persister will save things someplace, somehow
type Persister interface {
	Get(key string, value any) error
	Put(key string, value any) error
	Delete(key string) error
	PrefixScan(prefix string) ([]string, error)
	Sync() error
	io.Closer
}
