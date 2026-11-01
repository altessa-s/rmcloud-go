// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import "encoding/json/v2"

// rootWire is the root pointer as it arrives. Hash and generation are
// pointers so a missing key is not the zero value. missingHashes is a
// pointer to a slice so an absent or null list stays nil and a present
// empty list is a count of zero.
type rootWire struct {
	Hash                   *string   `json:"hash"`
	Generation             *int64    `json:"generation"`
	SchemaVersion          *int      `json:"schemaVersion"`
	DeletedImmutableAction *string   `json:"deletedImmutableAction"`
	MaxNetworkJobs         *int      `json:"maxNetworkJobs"`
	HasMissingHashes       *bool     `json:"hasMissingHashes"`
	MissingHashes          *[]string `json:"missingHashes"`
	DisableHTTP2           *bool     `json:"disableHTTP2"`
}

// ParseRoot reads a root response. Hash and generation are required. A
// missing key, a null, or a value of the wrong type is a malformed
// response. Unknown fields are ignored. The advisory keeps the count of
// missing hashes, not the names.
func ParseRoot(data []byte) (Root, RootAdvisory, error) {
	var wire rootWire
	if err := json.Unmarshal(data, &wire); err != nil || wire.Hash == nil || wire.Generation == nil {
		return Root{}, RootAdvisory{}, MalformedResponse("root response")
	}
	adv := RootAdvisory{
		SchemaVersion:          wire.SchemaVersion,
		DeletedImmutableAction: wire.DeletedImmutableAction,
		MaxNetworkJobs:         wire.MaxNetworkJobs,
		HasMissingHashes:       wire.HasMissingHashes,
		DisableHTTP2:           wire.DisableHTTP2,
	}
	if wire.MissingHashes != nil {
		adv.MissingHashes = new(len(*wire.MissingHashes))
	}
	return Root{Hash: *wire.Hash, Generation: *wire.Generation}, adv, nil
}
