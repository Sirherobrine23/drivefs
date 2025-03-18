package slice

import "slices"

type Slice[T any] []T

func (s Slice[T]) Orin() []T { return s }

// Filter elements from slice and return new slice
func (s Slice[T]) Filter(filter func(input T) bool) Slice[T] {
	newBook := []T{}
	for _, book := range s {
		if filter(book) {
			newBook = append(newBook, book)
		}
	}
	return newBook
}

// The at() method of Array instances takes an integer value and returns the item at that index, allowing for positive and negative integers. Negative integers count back from the last item in the array.
func (s Slice[T]) At(index int) T {
	if len(s) == 0 {
		return *new(T)
	}

	if index > len(s) {
		return *new(T)
	} else if index < 0 {
		index = len(s) + index
	}
	return s[min(index, len(s)-1)]
}

func (s Slice[T]) Find(call func(input T) bool) T {
	for _, book := range s {
		if call(book) {
			return book
		}
	}
	return *new(T)
}

func (s Slice[T]) FindIndex(call func(input T) bool) int {
	for index, book := range s {
		if call(book) {
			return index
		}
	}
	return -1
}

func (s Slice[T]) FindLast(call func(input T) bool) T {
	for i := len(s) - 1; i >= 0; i-- {
		if call(s[i]) {
			return s[i]
		}
	}
	return *new(T)
}

func (s Slice[T]) FindLastIndex(call func(input T) bool) int {
	for i := len(s) - 1; i >= 0; i-- {
		if call(s[i]) {
			return i
		}
	}
	return -1
}

func (s Slice[T]) Slice(start, end int) Slice[T] {
	if end == 0 {
		if start > len(s) {
			return []T{}
		}
		return s[start:]
	}
	if start > len(s) {
		return []T{}
	} else if end > len(s) {
		end = len(s) - 1
	} else if start < 0 {
		start = len(s) + start
	}
	if end < 0 {
		end = len(s) + end
	}
	return s[min(start, len(s)-1):min(end, len(s)-1)]
}

func (s *Slice[T]) Shift() T {
	if len(*s) == 0 {
		return *new(T)
	}
	first := (*s)[0]
	*s = (*s)[1:]
	return first
}

func (s *Slice[T]) Pop() T {
	if len(*s) == 0 {
		return *new(T)
	}
	last := (*s)[len(*s)-1]
	*s = (*s)[:len(*s)-1]
	return last
}

func (s *Slice[T]) Unshift(value T) Slice[T] {
	*s = append([]T{value}, *s...)
	return *s
}

func (s *Slice[T]) Push(value T) Slice[T] {
	*s = append(*s, value)
	return *s
}

func (s *Slice[T]) Sort(fn func(a, b T) int) {
	slices.SortFunc(*s, fn)
}