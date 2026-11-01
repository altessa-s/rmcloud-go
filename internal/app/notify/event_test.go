// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package notify

import (
	"bytes"
	"strings"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

const sampleEnvelope = `{"message":{"attributes":{"auth0UserID":"auth0|x","event":"SyncComplete","sourceDeviceID":"device-1"},"messageId":"1"},"subscription":"s"}`

func textFrame(text string) port.Frame {
	return port.Frame{Textual: true, Text: text}
}

func mustParse(t *testing.T, frame port.Frame, own *string) Event {
	t.Helper()
	event, ok := parse(frame, own)
	if !ok {
		t.Fatalf("unreadable frame %q", frame.Text)
	}
	return event
}

func mustUnreadable(t *testing.T, frame port.Frame) {
	t.Helper()
	if _, ok := parse(frame, nil); ok {
		t.Fatal("parsed")
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("sample", func(t *testing.T) {
		t.Parallel()
		event := mustParse(t, textFrame(sampleEnvelope), nil)
		if event.Kind != KindSyncComplete || !event.IsSyncComplete() {
			t.Fatalf("kind %q library %v", event.Kind, event.IsSyncComplete())
		}
		if event.Device != "device-1" || event.Attributes["auth0UserID"] != "auth0|x" {
			t.Fatalf("device %q auth %q", event.Device, event.Attributes["auth0UserID"])
		}
		if !strings.Contains(event.Frame.Text, "subscription") {
			t.Fatalf("frame %q", event.Frame.Text)
		}
		if (Event{}).IsSyncComplete() {
			t.Fatal("zero event changes the library")
		}
		for _, raw := range []string{
			"not json",
			"{}",
			`{"message":{}}`,
			`{"message":{"attributes":null}}`,
			`{"message":{"attributes":[]}}`,
			`{"message":{"attributes":{"event":"SyncComplete","event":"Nope"}}}`,
			"{\"message\":{\"attributes\":{\"event\":\"Sync\xffComplete\"}}}",
		} {
			mustUnreadable(t, textFrame(raw))
		}
		trailing := mustParse(t, textFrame(sampleEnvelope+" {\"extra\":true}"), nil)
		if trailing.Kind != KindSyncComplete {
			t.Fatalf("trailing kind %q", trailing.Kind)
		}
		mustUnreadable(t, port.Frame{Textual: true, Text: ""})
		mustUnreadable(t, port.Frame{})
	})

	t.Run("values", func(t *testing.T) {
		t.Parallel()
		raw := `{"message":{"attributes":{"n":1,"f":1.50,"yes":true,"no":false,"z":null,"obj":{"a":1},"arr":[ 2, 1, {"b": 0.10} ]}}}`
		event := mustParse(t, textFrame(raw), nil)
		want := map[string]string{
			"n":   "1",
			"f":   "1.50",
			"yes": "true",
			"no":  "false",
			"z":   "<null>",
			"obj": `{"a":1}`,
			"arr": `[2,1,{"b":0.10}]`,
		}
		for key, value := range want {
			if event.Attributes[key] != value {
				t.Errorf("attribute %s = %q, want %q", key, event.Attributes[key], value)
			}
		}
		empty := mustParse(t, textFrame(`{"message":{"attributes":{}}}`), nil)
		if empty.Attributes == nil || len(empty.Attributes) != 0 {
			t.Fatalf("attributes %#v", empty.Attributes)
		}
	})

	t.Run("fields", func(t *testing.T) {
		t.Parallel()
		blankID := mustParse(t, textFrame(`{"message":{"attributes":{"id":"","ID":"from-ID"}}}`), nil)
		if blankID.DocumentID != "" {
			t.Fatalf("document %q", blankID.DocumentID)
		}
		fallback := mustParse(t, textFrame(`{"message":{"attributes":{"ID":"from-ID"}}}`), nil)
		if fallback.DocumentID != "from-ID" {
			t.Fatalf("document %q", fallback.DocumentID)
		}
		named := mustParse(t, textFrame(`{"message":{"attributes":{"vissibleName":"typo","visibleName":"correct"}}}`), nil)
		if named.VisibleName != "typo" || named.Attributes["visibleName"] != "correct" {
			t.Fatalf("visible %q map %q", named.VisibleName, named.Attributes["visibleName"])
		}
		blankDesc := mustParse(t, textFrame(`{"message":{"attributes":{"sourceDeviceDesc":"","sourceDeviceID":"device-1"}}}`), nil)
		if blankDesc.Device != "" {
			t.Fatalf("device %q", blankDesc.Device)
		}
		nullDesc := mustParse(t, textFrame(`{"message":{"attributes":{"sourceDeviceDesc":null,"sourceDeviceID":"device-1"}}}`), nil)
		if nullDesc.Device != "<null>" {
			t.Fatalf("device %q", nullDesc.Device)
		}
	})

	t.Run("from this device", func(t *testing.T) {
		t.Parallel()
		missing := textFrame(`{"message":{"attributes":{"event":"SyncComplete"}}}`)
		if mustParse(t, missing, nil).FromThisDevice {
			t.Fatal("nil own matched a missing source")
		}
		if mustParse(t, missing, new("device-1")).FromThisDevice {
			t.Fatal("own id matched a missing source")
		}
		same := textFrame(`{"message":{"attributes":{"sourceDeviceID":"device-1"}}}`)
		if !mustParse(t, same, new("device-1")).FromThisDevice {
			t.Fatal("equal source did not match")
		}
		blank := textFrame(`{"message":{"attributes":{"sourceDeviceID":""}}}`)
		if !mustParse(t, blank, new("")).FromThisDevice {
			t.Fatal("empty source did not match")
		}
		nullSource := textFrame(`{"message":{"attributes":{"sourceDeviceID":null}}}`)
		if !mustParse(t, nullSource, new("<null>")).FromThisDevice {
			t.Fatal("null source did not match")
		}
		other := textFrame(`{"message":{"attributes":{"sourceDeviceID":"device-1"}}}`)
		if mustParse(t, other, new("device-2")).FromThisDevice {
			t.Fatal("different source matched")
		}
		described := mustParse(t, textFrame(`{"message":{"attributes":{"sourceDeviceID":"device-1","sourceDeviceDesc":"laptop"}}}`), new("device-1"))
		if !described.FromThisDevice || described.Device != "laptop" {
			t.Fatalf("from %v device %q", described.FromThisDevice, described.Device)
		}
	})

	t.Run("kinds", func(t *testing.T) {
		t.Parallel()
		kinds := []Kind{
			KindSyncComplete,
			KindSearchIndexUpdated,
			KindIntegrationsUpdated,
			KindPasscodeReset,
			KindPasscodeResetApproved,
			KindPasscodeResetDenied,
		}
		for _, kind := range kinds {
			event := mustParse(t, textFrame(`{"message":{"attributes":{"event":"`+string(kind)+`"}}}`), nil)
			if event.Kind != kind || event.Name != string(kind) {
				t.Fatalf("kind %q name %q", event.Kind, event.Name)
			}
			if event.IsSyncComplete() != (kind == KindSyncComplete) {
				t.Fatalf("%s library %v", kind, event.IsSyncComplete())
			}
		}
		unknown := mustParse(t, textFrame(`{"message":{"attributes":{"event":"Nope"}}}`), nil)
		if unknown.Name != "Nope" || unknown.Kind != "" || unknown.IsSyncComplete() {
			t.Fatalf("name %q kind %q library %v", unknown.Name, unknown.Kind, unknown.IsSyncComplete())
		}
	})

	t.Run("binary", func(t *testing.T) {
		t.Parallel()
		payload := []byte(sampleEnvelope)
		binary := port.Frame{Binary: payload}
		event := mustParse(t, binary, nil)
		if event.Frame.Textual || event.Kind != KindSyncComplete {
			t.Fatalf("textual %v kind %q", event.Frame.Textual, event.Kind)
		}
		if !bytes.Equal(event.Frame.Binary, payload) || event.Frame.Text != "" {
			t.Fatalf("binary %q text %q", event.Frame.Binary, event.Frame.Text)
		}
		mixed := port.Frame{Textual: true, Text: sampleEnvelope, Binary: []byte("garbage")}
		parsed := mustParse(t, mixed, nil)
		if !parsed.Frame.Textual || parsed.Frame.Text != sampleEnvelope || string(parsed.Frame.Binary) != "garbage" {
			t.Fatalf("frame %+v", parsed.Frame)
		}
		if parsed.Kind != KindSyncComplete {
			t.Fatalf("kind %q", parsed.Kind)
		}
	})
}

func FuzzParseEvent(f *testing.F) {
	f.Add(sampleEnvelope, "device-1")
	f.Add(`{"message":{"attributes":{"n":1,"f":1.50,"yes":true,"no":false,"z":null,"obj":{"a":1}}}}`, "")
	f.Add("{}", "")
	f.Fuzz(func(_ *testing.T, raw, own string) {
		var ownPtr *string
		if own != "" {
			ownPtr = &own
		}
		ev, ok := parse(textFrame(raw), ownPtr)
		if ok {
			_ = ev.IsSyncComplete()
		}
	})
}
