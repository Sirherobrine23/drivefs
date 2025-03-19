package cache

import (
	"iter"
	"sync"
	"time"
)

type MemoryValue[T any] struct {
	ValidTime time.Time
	Value     T
}

type Memory[T any] struct {
	Vmap   map[string]*MemoryValue[T] // Memory values
	locker sync.RWMutex               // sync to map
}

func NewMemory[T any]() Cache[T] {
	return &Memory[T]{map[string]*MemoryValue[T]{}, sync.RWMutex{}}
}

func (mem *Memory[T]) Delete(key string) error {
	mem.locker.Lock()
	defer mem.locker.Unlock()
	delete(mem.Vmap, key)
	return nil
}

func (mem *Memory[T]) Set(ttl time.Duration, key string, value T) error {
	mem.locker.Lock()
	defer mem.locker.Unlock()
	mem.Vmap[key] = &MemoryValue[T]{time.Now().Add(ttl), value}
	return nil
}

func (mem *Memory[T]) Get(key string) (value T, err error) {
	mem.locker.RLock()
	defer mem.locker.RUnlock()
	if v, ok := mem.Vmap[key]; ok && v != nil && v.ValidTime.Compare(time.Now()) != 1 {
		return v.Value, nil
	}
	return
}

func (mem *Memory[T]) Values() (iter.Seq2[string, T], error) {
	return func(yield func(string, T) bool) {
		mem.locker.RLock()
		defer mem.locker.RUnlock()
		for key, value := range mem.Vmap {
			if value == nil || value.ValidTime.Compare(time.Now()) != 1 {
				continue
			}
			if !yield(key, value.Value) {
				return
			}
		}
	}, nil
}

func (mem *Memory[T]) Flush() error {
	mem.locker.Lock()
	defer mem.locker.Unlock()
	for key, value := range mem.Vmap {
		if value == nil || value.ValidTime.Compare(time.Now()) != 1 {
			delete(mem.Vmap, key)
		}
	}
	return nil
}
