package ipc

import (
	"context"
	"sync"
)

// MessageRouter continuously reads from a Bridge and dispatches messages
// to the current subscriber. It supports cancellation of in-flight queries
// while allowing permission prompts and other message types to flow through.
type MessageRouter struct {
	bridge *Bridge
	ctx    context.Context

	mu          sync.Mutex
	incoming    chan ClientMessage // buffered channel for incoming messages
	pending     []ClientMessage
	cancelFunc  context.CancelFunc
	shutdownErr error
	stopped     bool
}

// NewMessageRouter creates a router that reads from the bridge in a
// background goroutine. Call Stop() when done.
func NewMessageRouter(ctx context.Context, bridge *Bridge) *MessageRouter {
	r := &MessageRouter{
		bridge:   bridge,
		ctx:      ctx,
		incoming: make(chan ClientMessage, 16),
	}
	go r.readLoop()
	return r
}

func (r *MessageRouter) readLoop() {
	for {
		msg, err := r.bridge.ReadMessage(r.ctx)
		if err != nil {
			r.mu.Lock()
			r.stopped = true
			r.shutdownErr = err
			r.mu.Unlock()
			close(r.incoming)
			return
		}
		if msg.Type == MsgCancel {
			r.mu.Lock()
			fn := r.cancelFunc
			r.mu.Unlock()
			if fn != nil {
				fn()
			}
			continue
		}
		r.incoming <- msg
	}
}

// Next blocks until the next message arrives or context is cancelled.
// During a query, cancel messages trigger the registered cancel function.
func (r *MessageRouter) Next(ctx context.Context) (ClientMessage, error) {
	for {
		r.mu.Lock()
		if len(r.pending) > 0 {
			msg := r.pending[0]
			r.pending = r.pending[1:]
			r.mu.Unlock()
			return msg, nil
		}
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return ClientMessage{}, ctx.Err()
		case msg, ok := <-r.incoming:
			if !ok {
				r.mu.Lock()
				err := r.shutdownErr
				r.mu.Unlock()
				return ClientMessage{}, err
			}

			if msg.Type == MsgCancel {
				r.mu.Lock()
				fn := r.cancelFunc
				r.mu.Unlock()
				if fn != nil {
					fn()
					// Don't return the cancel message; the context
					// cancellation propagates to the caller.
					continue
				}
				// No active query — ignore stale cancel
				continue
			}
			return msg, nil
		}
	}
}

// Requeue prepends messages so the next caller to Next receives them before
// newly-read bridge messages.
func (r *MessageRouter) Requeue(msgs ...ClientMessage) {
	if len(msgs) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	queued := append([]ClientMessage(nil), msgs...)
	r.pending = append(queued, r.pending...)
}

// SetCancelFunc registers a function to call when a cancel message arrives.
// Pass nil to clear it.
func (r *MessageRouter) SetCancelFunc(fn context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelFunc = fn
}
