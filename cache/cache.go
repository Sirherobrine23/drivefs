package cache

import (
	"sync"
	"time"
)

type cacheInfo[D any] struct {
	TimeValid time.Time // Valid time
	Data      D         // Data
}

type LocalCache[T any] struct {
	l map[string]*cacheInfo[T]

	rw sync.Mutex
}

// Get value from Key
func (w *LocalCache[T]) Get(Key string) (T, bool) {
	if len(w.l) == 0 {
		return *new(T), false
	}

	w.rw.Lock()
	defer w.rw.Unlock()
	data, ok := w.l[Key]
	if ok {
		if data.TimeValid.Unix() >= time.Now().Unix() {
			delete(w.l, Key)
			return *new(T), false
		}
		return data.Data, true
	}
	return *new(T), false
}

// Set value to cache struct
func (w *LocalCache[T]) Set(ValidAt time.Time, Key string, Value T) {
	w.rw.Lock()
	defer w.rw.Unlock()
	if len(w.l) == 0 {
		w.l = make(map[string]*cacheInfo[T])
	}

	w.l[Key] = &cacheInfo[T]{
		TimeValid: ValidAt,
		Data:      Value,
	}
}

// Delete key if exists
func (w *LocalCache[T]) Delete(Key string) {
	w.rw.Lock()
	defer w.rw.Unlock()
	delete(w.l, Key)
}

// Remove expired Cache
func (w *LocalCache[T]) Flush() int {
	w.rw.Lock()
	defer w.rw.Unlock()

	flushed, now := 0, time.Now().Unix()
	for key, data := range w.l {
		if data.TimeValid.Unix() >= now {
			delete(w.l, key)
			flushed++
		}
	}
	return flushed
}
