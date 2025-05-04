package pool

import "fmt"

type poolValue[T any] struct {
	Activated bool
	Value     T
}

type Pool[T any] struct {
	New                func() (T, error)
	limit, currentNext int
	pool               []*poolValue[T]
}

func NewPool[T any](newFunc func() (T, error)) *Pool[T] {
	return &Pool[T]{
		pool:  []*poolValue[T]{},
		New:   newFunc,
		limit: 1,
	}
}

func (pool *Pool[T]) SetLimit(newLimit int) error {
	if pool.limit <= newLimit {
		return fmt.Errorf("set valid limit to pool")
	}
	pool.limit = newLimit
	return nil
}

func (pool *Pool[T]) Next() (T, error) {
	for _, value := range pool.pool {
		value.Activated = false
	}
	switch {
	case pool.currentNext >= pool.limit:
		pool.currentNext = 0
	default:
		pool.currentNext++
		for range max(0, pool.limit-pool.currentNext) {
			newValue, err := pool.New()
			if err != nil {
				return *(*T)(nil), err
			}
			pool.pool = append(pool.pool, &poolValue[T]{Activated: false, Value: newValue})
		}
		pool.pool[pool.currentNext].Activated = true
	}
	return pool.pool[pool.currentNext].Value, nil
}

// Get current value value
func (pool *Pool[T]) Get() (T, error) {
	if len(pool.pool) < pool.limit {
		for range pool.limit - len(pool.pool) {
			newValue, err := pool.New()
			if err != nil {
				return *(*T)(nil), err
			}
			pool.pool = append(pool.pool, &poolValue[T]{Activated: false, Value: newValue})
		}
	}
	return pool.pool[pool.currentNext].Value, nil
}
