// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"encoding/json/v2"
	"strings"
)

// CheckFilesCall names the check-files request in a [NothingToAskError] or
// a [MissingHashError].
const CheckFilesCall = "check-files"

// checkFilesRequest is the body the service needs: the list of hashes.
type checkFilesRequest struct {
	Files []string `json:"files"`
}

type checkFilesWire struct {
	MissingFiles *[]string `json:"missingFiles"`
}

// CheckFilesBody is the request body for hashes. An empty list or a hash
// that is not hex is refused: the service answers an empty list with 400,
// which says nothing about any hash.
func CheckFilesBody(hashes []string) ([]byte, error) {
	if len(hashes) == 0 {
		return nil, NothingToAsk(CheckFilesCall)
	}
	for _, hash := range hashes {
		if !IsHash(hash) {
			return nil, MissingHash(CheckFilesCall)
		}
	}
	return json.Marshal(checkFilesRequest{Files: hashes})
}

// ParseMissingFiles reads a check-files answer. The list is required, and
// each hash in it must be one that was asked about; hex case is not
// significant. The hashes are returned as asked, in the order of the answer;
// a hash asked twice in different case comes back in the later spelling.
func ParseMissingFiles(data []byte, asked []string) ([]string, error) {
	var wire checkFilesWire
	if err := json.Unmarshal(data, &wire); err != nil || wire.MissingFiles == nil {
		return nil, MalformedResponse("check-files response")
	}
	byLower := make(map[string]string, len(asked))
	for _, hash := range asked {
		byLower[strings.ToLower(hash)] = hash
	}
	missing := make([]string, 0, len(*wire.MissingFiles))
	for _, hash := range *wire.MissingFiles {
		original, ok := byLower[strings.ToLower(hash)]
		if !ok {
			return nil, MalformedResponse("check-files response names a hash that was not asked about")
		}
		missing = append(missing, original)
	}
	return missing, nil
}
