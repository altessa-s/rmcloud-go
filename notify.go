// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud

import (
	"context"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

// NotificationKind says what a [Notification] reports.
type NotificationKind int

const (
	// NotificationConnected means the socket is up. It is sent again after
	// every reconnect.
	NotificationConnected NotificationKind = iota + 1
	// NotificationEvent is a parsed frame. SyncComplete, Name,
	// DocumentID, VisibleName, and FromThisDevice describe it.
	NotificationEvent
	// NotificationUnreadable means the frame did not parse. It is still a
	// sign that the account changed.
	NotificationUnreadable
	// NotificationDropped means the socket went down. Err is the cause.
	NotificationDropped
)

// String names the kind.
func (k NotificationKind) String() string {
	switch k {
	case NotificationConnected:
		return "connected"
	case NotificationEvent:
		return "event"
	case NotificationUnreadable:
		return "unreadable"
	case NotificationDropped:
		return "dropped"
	default:
		return "unknown"
	}
}

// Notification is one signal from the notification socket. Kind selects
// which of the other fields are set; the rest are zero.
type Notification struct {
	Kind NotificationKind

	// SyncComplete reports a SyncComplete event: the account's documents
	// changed and the root is worth re-reading.
	SyncComplete bool
	// Name is the event name as the service sent it.
	Name string
	// DocumentID is the document the event is about, if any.
	DocumentID string
	// VisibleName is the document's display name, if the event carries one.
	VisibleName string
	// FromThisDevice reports an event this client's own device caused.
	FromThisDevice bool

	// Frame is the socket frame as it arrived, for an event and for a frame
	// that did not parse; nil for the other kinds.
	Frame []byte

	// Err is why the socket dropped.
	Err error
}

// StartNotifications stores on and starts the stream.
//
// on runs on the stream goroutine and must not call [Client.StopNotifications]
// or [Client.Close]: both wait for that goroutine. A nil callback is safe.
// A second call replaces the callback. The stream ignores Start while a run
// is still tracked.
func (c *Client) StartNotifications(ctx context.Context, on func(Notification)) {
	c.mu.Lock()
	c.onNote = on
	c.mu.Unlock()
	c.notes.Start(ctx)
}

// StopNotifications ends the run and waits. It is safe without Start and safe twice.
func (c *Client) StopNotifications() {
	c.notes.Stop()
}

// deliver maps a stream signal onto the public callback. The callback is
// read under the lock and invoked without it.
func (c *Client) deliver(sig port.Signal) {
	note, ok := notificationOf(sig)
	if !ok {
		return
	}
	c.mu.Lock()
	on := c.onNote
	c.mu.Unlock()
	if on != nil {
		on(note)
	}
}

// notificationOf keeps the public signal narrow. Unknown kinds are dropped.
func notificationOf(sig port.Signal) (Notification, bool) {
	switch sig.Kind {
	case port.SignalConnected:
		return Notification{Kind: NotificationConnected}, true
	case port.SignalEvent:
		ev := sig.Event
		return Notification{
			Kind:           NotificationEvent,
			SyncComplete:   ev.IsSyncComplete(),
			Name:           ev.Name,
			DocumentID:     ev.DocumentID,
			VisibleName:    ev.VisibleName,
			FromThisDevice: ev.FromThisDevice,
			Frame:          frameBytes(ev.Frame),
		}, true
	case port.SignalUnreadable:
		return Notification{Kind: NotificationUnreadable, Frame: frameBytes(sig.Frame)}, true
	case port.SignalDropped:
		return Notification{Kind: NotificationDropped, Err: sig.Err}, true
	default:
		return Notification{}, false
	}
}

// frameBytes is a frame's payload: the text of a textual frame, the bytes of
// a binary one.
func frameBytes(f port.Frame) []byte {
	if f.Textual {
		return []byte(f.Text)
	}
	return f.Binary
}
