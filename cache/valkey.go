package cache

import (
	"context"
	"iter"
	"time"

	"github.com/valkey-io/valkey-go"
)

type Valkey[T any] struct {
	Client valkey.Client
}

func NewValkey[T any](opt valkey.ClientOption) (Cache[T], error) {
	client, err := valkey.NewClient(opt)
	if err != nil {
		return nil, err
	}
	return Valkey[T]{Client: client}, nil
}

func (valkey Valkey[T]) Flush() error                 { return nil }
func (valkey Valkey[T]) Values() iter.Seq2[string, T] { return nil }

func (valkey Valkey[T]) Delete(key string) error {
	return valkey.Client.Do(context.Background(), valkey.Client.B().Del().Key(key).Build()).Error()
}

func (valkey Valkey[T]) Set(ttl time.Duration, key string, value T) error {
	data, err := ToString(value)
	if err != nil {
		return err
	}
	return valkey.Client.Do(context.Background(), valkey.Client.B().Set().Key(key).Value(data).Ex(ttl).Build()).Error()
}

func (valkey Valkey[T]) Get(key string) (T, error) {
	str, err := valkey.Client.Do(context.Background(), valkey.Client.B().Get().Key(key).Build()).ToString()
	if err != nil {
		return *new(T), err
	}
	return FromString[T](str)
}
