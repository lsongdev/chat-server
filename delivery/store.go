package delivery

import "context"

// Access is implemented by a layer above Delivery. It answers only the three
// routing questions the kernel needs; it does not define a permission model.
type Access interface {
	Routes(context.Context, string) ([]string, error)
	CanPublish(context.Context, string, string) error
	HistoryStart(context.Context, string, string) (int64, error)
}

// Store is the durable event source of truth. Append must allocate a room
// sequence and enforce publish idempotency atomically.
type Store interface {
	Append(context.Context, Publish) (Event, error)
	EventsAfter(context.Context, string, int64, int) ([]Event, error)
	HeadSequence(context.Context, string) (int64, error)
}
