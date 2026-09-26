package ipc

import (
	"context"
	"errors"
	"fmt"
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

	mu sync.Mutex
	// pending holds requeued messages, which are delivered first.
	pending []ClientMessage
	// incoming holds messages read from the bridge and not yet delivered, in
	// arrival order. It is unbounded on purpose: during a turn nothing reads
	// the router, and a reader that blocked on a full buffer would also stop
	// reading the cancel message the user sends to end that turn.
	incoming []ClientMessage
	// wake is signalled when incoming grows or the reader stops.
	wake        chan struct{}
	stopped     bool
	cancelFunc  context.CancelFunc
	shutdownErr error
}

// NewMessageRouter creates a router that reads from the bridge in a
// background goroutine. Cancel ctx to stop it.
func NewMessageRouter(ctx context.Context, bridge *Bridge) *MessageRouter {
	r := &MessageRouter{
		bridge: bridge,
		ctx:    ctx,
		wake:   make(chan struct{}, 1),
	}
	go r.readLoop()
	return r
}

func (r *MessageRouter) readLoop() {
	for {
		msg, err := r.bridge.ReadMessage(r.ctx)
		if messageErr, ok := errors.AsType[*MessageError](err); ok {
			// One bad message: report it and keep reading, since the stream
			// is still in step. A dropped message may be input the client
			// is waiting on, so the error ends that turn in the client.
			if emitErr := r.bridge.EmitError(fmt.Sprintf("message from the client was dropped: %v", messageErr), false); emitErr != nil {
				r.shutdown(emitErr)
				return
			}
			continue
		}
		if err != nil {
			r.shutdown(err)
			return
		}
		if msg.Type == MsgCancel {
			r.triggerCancel()
			continue
		}
		r.mu.Lock()
		r.incoming = append(r.incoming, msg)
		r.mu.Unlock()
		r.signal()
	}
}

func (r *MessageRouter) shutdown(err error) {
	r.mu.Lock()
	r.shutdownErr = err
	r.stopped = true
	r.mu.Unlock()
	r.signal()
}

// signal wakes a waiting Next without blocking. One pending wake-up is
// enough: Next drains the queue before it waits again.
func (r *MessageRouter) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
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
// Cancel messages never reach the caller: the reader hands them straight to
// the registered cancel function, or drops them when no query is running.
func (r *MessageRouter) Next(ctx context.Context) (ClientMessage, error) {
	for {
		r.mu.Lock()
		if len(r.pending) > 0 {
			msg := r.pending[0]
			r.pending = r.pending[1:]
			r.mu.Unlock()
			return msg, nil
		}
		if len(r.incoming) > 0 {
			msg := r.incoming[0]
			r.incoming = r.incoming[1:]
			r.mu.Unlock()
			return msg, nil
		}
		if r.stopped {
			err := r.shutdownErr
			r.mu.Unlock()
			return ClientMessage{}, err
		}
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return ClientMessage{}, ctx.Err()
		case <-r.wake:
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
