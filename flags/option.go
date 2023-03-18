package flags

type Optional[T any] struct {
	v    T
	some bool
}

func (o *Optional[T]) IsSome() bool {
	return o.some
}

func (o *Optional[T]) IsNone() bool {
	return !o.some
}

func (o *Optional[T]) Unwrap() T {
	if !o.some {
		panic("unwrap of none")
	}
	return o.v
}
