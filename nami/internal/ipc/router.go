package ipc

import (
	"context"
	"sync"
)

// MessageRouter continuously reads from a Bridge and dispatches messages
// to the current subscriber. It supports cancellation of in-flight queries
// while allowing permission prompts and other message types to flow through.
// The reader goroutine ends when the bridge fails or the router context is
// cancelled.
type MessageRouter struct {
	bridge *Bridge
	ctx    context.Context

	mu          sync.Mutex
	incoming    chan ClientMessage // buffered channel for incoming messages
	pending     []ClientMessage
	cancelFunc  context.CancelFunc
	shutdownErr error
}

// NewMessageRouter creates a router that reads from the bridge in a
// background goroutine. Cancel ctx to stop it.
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
			r.shutdown(err)
			return
		}
		if msg.Type == MsgCancel {
			r.triggerCancel()
			continue
		}
		// Nothing may consume the buffered channel once the engine is shutting
		// down, so the send has to lose to context cancellation or this
		// goroutine outlives the session.
		select {
		case r.incoming <- msg:
		case <-r.ctx.Done():
			r.shutdown(r.ctx.Err())
			return
		}
	}
}

func (r *MessageRouter) shutdown(err error) {
	r.mu.Lock()
	r.shutdownErr = err
	r.mu.Unlock()
	close(r.incoming)
}

// triggerCancel invokes the registered cancel function, if a query is running.
func (r *MessageRouter) triggerCancel() {
	r.mu.Lock()
	fn := r.cancelFunc
	r.mu.Unlock()
	if fn != nil {
		fn()
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

			// Cancel messages never reach the caller: either they cancel the
			// active query's context, or there is no query and they are stale.
			if msg.Type == MsgCancel {
				r.triggerCancel()
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
