package db

import (
	"sync"

	"github.com/goccy/go-json"

	badger "github.com/dgraph-io/badger/v4"
)

// Badger is a simple key value store
type Badger struct {
	db         *badger.DB
	sync.Mutex // for close only
}

// NewBadger gets a closable Key Value store
func NewBadger(path string) (*Badger, error) {
	k := &Badger{}
	if path == "" {
		path = "./"
	}
	opts := badger.DefaultOptions(path)
	opts.IndexCacheSize = 100 << 20 // 100 mb
	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}
	k.db = db

	return k, nil
}

// Sync to disk
func (kv *Badger) Sync() error {
	return kv.db.Sync()
}

// Close for io.Closer
func (kv *Badger) Close() error {
	kv.Lock()
	defer kv.Unlock()
	var err error
	if kv.db != nil {
		err = kv.db.Close()
		kv.db = nil
	}
	return err
}

// Get a value from the storage
func (kv *Badger) Get(key string, value any) error {
	err := kv.db.View(func(txn *badger.Txn) error {
		i, err := txn.Get([]byte(key))
		if err != nil {
			switch err {
			case badger.ErrKeyNotFound:
				return ErrNotFound
			default:
				return err
			}
		}
		return i.Value(func(b []byte) error {
			return json.Unmarshal(b, value)
		})
	})
	return err
}

// Put a value in the storage (upsert)
func (kv *Badger) Put(key string, value any) error {
	err := kv.db.Update(func(txn *badger.Txn) error {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return txn.Set([]byte(key), b)
	})
	return err
}

// Delete a k/v from the DB
func (kv *Badger) Delete(key string) error {
	err := kv.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(key))
	})
	return err
}

// PrefixScan for matching keys
func (kv *Badger) PrefixScan(prefix string) ([]string, error) {
	found := []string{}
	err := kv.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Seek([]byte(prefix)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			item := it.Item()
			found = append(found, string(item.Key()))
		}
		return nil
	})
	return found, err
}
