// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package port

// What the notification stream hands its caller: the output boundary of the
// notify use case.

// EventKind is an event name this client understands.
// An unknown word leaves an [Event]'s Kind empty and is still delivered.
type EventKind string

// The event names this client recognizes.
const (
	EventSyncComplete          EventKind = "SyncComplete"
	EventSearchIndexUpdated    EventKind = "SearchIndexUpdated"
	EventIntegrationsUpdated   EventKind = "IntegrationsUpdated"
	EventPasscodeReset         EventKind = "PasscodeReset"
	EventPasscodeResetApproved EventKind = "PasscodeResetApproved"
	EventPasscodeResetDenied   EventKind = "PasscodeResetDenied"
)

// Event is one parsed notification.
//
// Name is the event attribute. An empty name is logged as unnamed.
// Kind is empty when the word is unknown; that is still an event.
// Device prefers sourceDeviceDesc when that key is present, including an
// empty or null value, and otherwise uses sourceDeviceID.
// DocumentID prefers id over ID, and an empty id wins.
// VisibleName is the vissibleName attribute, in the wire spelling.
// FromThisDevice is true only when this device's id and sourceDeviceID
// are both present and equal. Two absences do not match.
// Attributes keeps every attribute. Frame is the raw socket frame.
type Event struct {
	Name           string
	Kind           EventKind
	Device         string
	DocumentID     string
	VisibleName    string
	SourceDeviceID string
	FromThisDevice bool
	Attributes     map[string]string
	Frame          Frame
}

// IsSyncComplete reports a SyncComplete event: the account's documents
// changed.
func (e Event) IsSyncComplete() bool { return e.Kind == EventSyncComplete }

// SignalKind distinguishes a socket announcement from a frame.
type SignalKind int

const (
	// SignalConnected means the socket is up. It is sent again after every reconnect.
	SignalConnected SignalKind = iota
	// SignalEvent is a parsed frame, including one from this device.
	SignalEvent
	// SignalUnreadable means the wrapper did not parse. The frame is the payload.
	SignalUnreadable
	// SignalDropped means the socket went down. Err is the cause.
	SignalDropped
)

// Signal is one announcement from the stream goroutine.
// The callback must not call Stop: Stop waits for that goroutine.
type Signal struct {
	Kind  SignalKind
	Event Event
	Frame Frame
	Err   error
}
