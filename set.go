package goply

func newSet[T comparable](items ...T) set[T] {
	s := set[T]{
		data: map[T]struct{}{},
	}
	s.Add(items...)
	return s
}

type set[T comparable] struct {
	data map[T]struct{}
}

func (s *set[T]) Add(items ...T) {
	for _, i := range items {
		s.data[i] = struct{}{}
	}
}

func (s *set[T]) Remove(items ...T) {
	for _, i := range items {
		delete(s.data, i)
	}
}

func (s *set[T]) Contains(item T) bool {
	_, ok := s.data[item]
	return ok
}
