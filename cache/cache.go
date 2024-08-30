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

func NewCacheMap[T any]() *LocalCache[T] {
	return &LocalCache[T]{
		l: make(map[string]*cacheInfo[T]),
	}
}

// Get value from Key
func (w *LocalCache[T]) Get(Key string) (T, bool) {
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
	w.l[Key] = &cacheInfo[T]{
		TimeValid: ValidAt,
		Data:      Value,
	}
}

func (w *LocalCache[T]) Flush() int64 {
	w.rw.Lock()
	defer w.rw.Unlock()
	var flushed int64 = 0
	for key, data := range w.l {
		if data.TimeValid.Unix() >= time.Now().Unix() {
			delete(w.l, key)
			flushed++
		}
	}
	return flushed
}
