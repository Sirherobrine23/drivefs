package cache

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"time"
)

var (
	ErrNotExist error = errors.New("key not exists")
)

// Generic Cache interface
type Cache[T any] interface {
	Flush() error                                     // Remove all outdated values
	Delete(key string) error                          // Remove value from cache
	Set(ttl time.Duration, key string, value T) error // set new value or replace current value
	Get(key string) (T, error)                        // Get current value
	Values() (iter.Seq2[string, T], error)            // List all keys with u values
}

func ToString(v any) (string, error) {
	switch v := v.(type) {
	case encoding.TextMarshaler:
		data, err := v.MarshalText()
		if err != nil {
			return "", err
		}
		return string(data), nil
	case encoding.BinaryMarshaler:
		data, err := v.MarshalBinary()
		if err != nil {
			return "", err
		}
		return string(data), nil
	case json.Marshaler:
		data, err := v.MarshalJSON()
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func FromString[T any](value string) (target T, err error) {
	switch v := any(target).(type) {
	case encoding.TextUnmarshaler:
		err = v.UnmarshalText([]byte(value))
	case encoding.BinaryUnmarshaler:
		err = v.UnmarshalBinary([]byte(value))
	case json.Unmarshaler:
		err = v.UnmarshalJSON([]byte(value))
	default:
		err = json.Unmarshal([]byte(value), &target)
	}
	return
}

func PrintCache[T any](w io.Writer, cache Cache[T]) error {
	values, err := cache.Values()
	if err != nil {
		return err
	}

	for key, value := range values {
		if _, err := fmt.Fprintf(w, "Cache key: %s, value: %+v\n", key, value); err != nil {
			return err
		}
	}
	return nil
}
