package db

import (
	"errors"
	"fmt"
	"io"
	"time"
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

// Watchtime for holding watchtime info
type Watchtime struct {
	User string
	Time time.Duration
}

// Key for storing in a KV
func (w Watchtime) Key() string {
	return fmt.Sprintf("watchtime-%s", w.User)
}

// Points for holding user points
type Points struct {
	User   string
	Points uint64
}

// Key for storing in a KV
func (w Points) Key() string {
	return fmt.Sprintf("points-%s", w.User)
}
