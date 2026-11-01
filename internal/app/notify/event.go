// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package notify

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Kind, Event and their constants are the port's: the facade reads them
// without importing this package.
type (
	Kind  = port.EventKind
	Event = port.Event
)

// The event names this client recognizes.
const (
	KindSyncComplete          = port.EventSyncComplete
	KindSearchIndexUpdated    = port.EventSearchIndexUpdated
	KindIntegrationsUpdated   = port.EventIntegrationsUpdated
	KindPasscodeReset         = port.EventPasscodeReset
	KindPasscodeResetApproved = port.EventPasscodeResetApproved
	KindPasscodeResetDenied   = port.EventPasscodeResetDenied
)

// envelope is the pub/sub wrapper. Only the attributes are read.
type envelope struct {
	Message *struct {
		Attributes attributes `json:"attributes"`
	} `json:"message"`
}

// attributes decodes the attribute object straight into strings, through
// [stringify]'s rules, so no raw copy of each value is kept on the way.
// The decoder still refuses duplicate names and invalid UTF-8.
type attributes map[string]string

var errNotAnObject = errors.New("notify: attributes are not an object")

// UnmarshalJSONFrom implements json.UnmarshalerFrom.
func (a *attributes) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case 'n':
		*a = nil
		_, err := dec.ReadToken()
		return err
	case '{':
	default:
		if _, err := dec.ReadValue(); err != nil {
			return err
		}
		return errNotAnObject
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	attrs := make(attributes)
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		// A token is void after the next decoder call: take the name first.
		name := tok.String()
		value, err := attributeValue(dec)
		if err != nil {
			return err
		}
		attrs[name] = value
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	*a = attrs
	return nil
}

// attributeValue reads one value as [stringify] describes it.
func attributeValue(dec *jsontext.Decoder) (string, error) {
	switch dec.PeekKind() {
	case 'n':
		_, err := dec.ReadToken()
		return nullText, err
	case '"':
		tok, err := dec.ReadToken()
		if err != nil {
			return "", err
		}
		return tok.String(), nil
	default:
		value, err := dec.ReadValue()
		if err != nil {
			return "", err
		}
		// The value is a view into the decoder's buffer; compact a copy.
		return stringify(slices.Clone(value)), nil
	}
}

// parse reads one pub/sub wrapper. false means the frame is unreadable,
// which is a wake, not a failure. own is this device's id; nil means unknown.
// Only the first JSON value of the frame is read; what follows it is not.
func parse(frame port.Frame, own *string) (Event, bool) {
	var env envelope
	dec := jsontext.NewDecoder(frameReader(frame))
	if err := json.UnmarshalDecode(dec, &env); err != nil || env.Message == nil || env.Message.Attributes == nil {
		return Event{}, false
	}
	attrs := map[string]string(env.Message.Attributes)
	present := func(key string) bool {
		_, ok := attrs[key]
		return ok
	}
	event := Event{
		Name:           attrs["event"],
		Kind:           knownKind(attrs["event"]),
		SourceDeviceID: attrs["sourceDeviceID"],
		VisibleName:    attrs["vissibleName"],
		Attributes:     attrs,
		Frame:          frame,
	}
	if present("sourceDeviceDesc") {
		event.Device = attrs["sourceDeviceDesc"]
	} else {
		event.Device = attrs["sourceDeviceID"]
	}
	if present("id") {
		event.DocumentID = attrs["id"]
	} else {
		event.DocumentID = attrs["ID"]
	}
	if own != nil && present("sourceDeviceID") && *own == attrs["sourceDeviceID"] {
		event.FromThisDevice = true
	}
	return event, true
}

// frameReader reads the frame without copying a textual one.
func frameReader(frame port.Frame) io.Reader {
	if frame.Textual {
		return strings.NewReader(frame.Text)
	}
	return bytes.NewReader(frame.Binary)
}

// nullText is the desktop's description of JSON null.
const nullText = "<null>"

func knownKind(name string) Kind {
	switch Kind(name) {
	case KindSyncComplete, KindSearchIndexUpdated, KindIntegrationsUpdated,
		KindPasscodeReset, KindPasscodeResetApproved, KindPasscodeResetDenied:
		return Kind(name)
	default:
		return ""
	}
}

// stringify keeps a non-string attribute. Null is the desktop's description
// of JSON null. A number keeps the digits that arrived. An object or array
// is the text that arrived, without its whitespace.
func stringify(value jsontext.Value) string {
	switch value.Kind() {
	case jsontext.KindNull:
		return nullText
	case jsontext.KindString:
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return ""
		}
		return s
	default:
		if err := value.Compact(); err != nil {
			return ""
		}
		return string(value)
	}
}
