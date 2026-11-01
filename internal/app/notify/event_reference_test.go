// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package notify

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

// parseReference is the straightforward definition parse must keep matching:
// every attribute decoded raw, then described by stringify.
func parseReference(frame port.Frame, own *string) (Event, bool) {
	var env struct {
		Message *struct {
			Attributes map[string]jsontext.Value `json:"attributes"`
		} `json:"message"`
	}
	data := frame.Binary
	if frame.Textual {
		data = []byte(frame.Text)
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	if err := json.UnmarshalDecode(dec, &env); err != nil || env.Message == nil || env.Message.Attributes == nil {
		return Event{}, false
	}
	attrs := make(map[string]string, len(env.Message.Attributes))
	for key, value := range env.Message.Attributes {
		attrs[key] = stringify(value)
	}
	pick := func(first, second string) string {
		if v, ok := attrs[first]; ok {
			return v
		}
		return attrs[second]
	}
	_, fromDevice := attrs["sourceDeviceID"]
	return Event{
		Name:           attrs["event"],
		Kind:           knownKind(attrs["event"]),
		SourceDeviceID: attrs["sourceDeviceID"],
		VisibleName:    attrs["vissibleName"],
		Attributes:     attrs,
		Frame:          frame,
		Device:         pick("sourceDeviceDesc", "sourceDeviceID"),
		DocumentID:     pick("id", "ID"),
		FromThisDevice: own != nil && fromDevice && *own == attrs["sourceDeviceID"],
	}, true
}

var referenceSeeds = []string{
	sampleEnvelope,
	`{"message":{"attributes":{"n":1,"f":1.50,"yes":true,"no":false,"z":null,"obj":{ "a" : [1, 2] }}}}`,
	`{"message":{"attributes":{"a":"x","a":"y"}}}`,
	`{"message":{"attributes":null}}`,
	`{"message":{"attributes":[]}}`,
	`{"message":{"attributes":"x"}}`,
	`{"message":{"attributes":{}}}`,
	`{"message":null}`,
	`{"message":{"attributes":{"s":"é\n\"q\""}}} trailing`,
	`{"message":{"attributes":{"id":"","ID":"upper","sourceDeviceDesc":null}}}`,
	`{"Message":{"attributes":{"event":"SyncComplete"}}}`,
	"{\"message\":{\"attributes\":{\"bad\":\"\xff\"}}}",
	`[]`,
	`{}`,
	``,
}

func sameEvent(a, b Event) bool {
	return maps.Equal(a.Attributes, b.Attributes) &&
		a.Name == b.Name && a.Kind == b.Kind && a.Device == b.Device &&
		a.DocumentID == b.DocumentID && a.VisibleName == b.VisibleName &&
		a.SourceDeviceID == b.SourceDeviceID && a.FromThisDevice == b.FromThisDevice
}

func checkAgainstReference(t *testing.T, raw string, own *string) {
	t.Helper()
	for _, frame := range []port.Frame{textFrame(raw), {Binary: []byte(raw)}} {
		got, ok := parse(frame, own)
		want, wantOK := parseReference(frame, own)
		if ok != wantOK || (ok && !sameEvent(got, want)) {
			t.Fatalf("parse(%q) = %+v %v, want %+v %v", raw, got, ok, want, wantOK)
		}
	}
}

func TestParseMatchesReference(t *testing.T) {
	t.Parallel()
	own := "device-1"
	for _, raw := range referenceSeeds {
		checkAgainstReference(t, raw, nil)
		checkAgainstReference(t, raw, &own)
	}
}

func FuzzParseEventMatchesReference(f *testing.F) {
	for _, raw := range referenceSeeds {
		f.Add(raw, "device-1")
	}
	f.Fuzz(func(t *testing.T, raw, own string) {
		var ownPtr *string
		if own != "" {
			ownPtr = &own
		}
		checkAgainstReference(t, raw, ownPtr)
	})
}
