// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"strconv"
	"strings"
)

// RootAdvisory is the half of the root pointer this client reads and does
// not obey. MissingHashes is the count of hashes the server listed, not the
// hashes. Nil and a pointer to false are different values; the struct is
// compared field by field for that reason.
type RootAdvisory struct {
	SchemaVersion          *int
	DeletedImmutableAction *string
	MaxNetworkJobs         *int
	HasMissingHashes       *bool
	MissingHashes          *int
	DisableHTTP2           *bool
}

// The root pointer's known schemaVersion values.
const (
	rootSchemaV3 = 3
	rootSchemaV4 = 4
)

// absent is how a remark spells a field the pointer did not carry.
const absent = "nothing"

// IsOrdinary reports the pointer's expected state. An absent field
// is ordinary. A maxNetworkJobs of 0 is present, so it is not. A false
// hasMissingHashes is ordinary; true is not. disableHTTP2 false is ordinary.
func (a RootAdvisory) IsOrdinary() bool {
	familiarSchema := a.SchemaVersion == nil || *a.SchemaVersion == rootSchemaV3 || *a.SchemaVersion == rootSchemaV4
	familiarAction := true
	if a.DeletedImmutableAction != nil {
		action := *a.DeletedImmutableAction
		familiarAction = strings.EqualFold(action, "rollback") || strings.EqualFold(action, "none")
	}
	missing := 0
	if a.MissingHashes != nil {
		missing = *a.MissingHashes
	}
	return familiarSchema && familiarAction &&
		a.MaxNetworkJobs == nil &&
		(a.HasMissingHashes == nil || !*a.HasMissingHashes) &&
		missing == 0 &&
		(a.DisableHTTP2 == nil || !*a.DisableHTTP2)
}

// Remarks is what is worth saying about this pointer given the one seen
// before. A nil previous is described whole. An equal previous says nothing.
func (a RootAdvisory) Remarks(previous *RootAdvisory) []string {
	if previous == nil {
		return a.description()
	}
	if a.equal(*previous) {
		return nil
	}
	var remarks []string
	if !sameInt(a.SchemaVersion, previous.SchemaVersion) {
		remarks = append(remarks, "schema "+spellInt(previous.SchemaVersion)+" → "+spellInt(a.SchemaVersion))
	}
	if !sameString(a.DeletedImmutableAction, previous.DeletedImmutableAction) {
		remarks = append(remarks, "deletedImmutableAction "+spellString(previous.DeletedImmutableAction)+" → "+spellString(a.DeletedImmutableAction))
	}
	if !sameInt(a.MaxNetworkJobs, previous.MaxNetworkJobs) {
		remarks = append(remarks,
			"maxNetworkJobs "+spellInt(previous.MaxNetworkJobs)+" → "+spellInt(a.MaxNetworkJobs)+
				" (not followed: this client keeps its own ceiling)")
	}
	if !sameBool(a.HasMissingHashes, previous.HasMissingHashes) || !sameInt(a.MissingHashes, previous.MissingHashes) {
		remarks = append(remarks, a.missingHashesRemark())
	}
	if !sameBool(a.DisableHTTP2, previous.DisableHTTP2) {
		remarks = append(remarks,
			"disableHTTP2 "+spellBool(previous.DisableHTTP2)+" → "+spellBool(a.DisableHTTP2)+
				" (not followed: the system transport negotiates the protocol)")
	}
	return remarks
}

func (a RootAdvisory) description() []string {
	var lines []string
	if a.SchemaVersion != nil {
		lines = append(lines, "schema "+strconv.Itoa(*a.SchemaVersion))
	}
	if a.DeletedImmutableAction != nil {
		lines = append(lines, "deletedImmutableAction "+*a.DeletedImmutableAction)
	}
	if a.MaxNetworkJobs != nil {
		lines = append(lines, "maxNetworkJobs "+strconv.Itoa(*a.MaxNetworkJobs)+" (not followed: this client keeps its own ceiling)")
	}
	if a.reportsMissing() {
		lines = append(lines, a.missingHashesRemark())
	}
	if a.DisableHTTP2 != nil {
		lines = append(lines, "disableHTTP2 "+strconv.FormatBool(*a.DisableHTTP2)+" (not followed: the system transport negotiates the protocol)")
	}
	return lines
}

func (a RootAdvisory) reportsMissing() bool {
	n := 0
	if a.MissingHashes != nil {
		n = *a.MissingHashes
	}
	return (a.HasMissingHashes != nil && *a.HasMissingHashes) || n > 0
}

func (a RootAdvisory) missingHashesRemark() string {
	if !a.reportsMissing() {
		return "the server reports no missing hashes"
	}
	count := "some"
	if a.MissingHashes != nil {
		count = strconv.Itoa(*a.MissingHashes)
	}
	return "the server reports " + count + " hashes it does not hold"
}

func (a RootAdvisory) equal(b RootAdvisory) bool {
	return sameInt(a.SchemaVersion, b.SchemaVersion) &&
		sameString(a.DeletedImmutableAction, b.DeletedImmutableAction) &&
		sameInt(a.MaxNetworkJobs, b.MaxNetworkJobs) &&
		sameBool(a.HasMissingHashes, b.HasMissingHashes) &&
		sameInt(a.MissingHashes, b.MissingHashes) &&
		sameBool(a.DisableHTTP2, b.DisableHTTP2)
}

func sameInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameBool(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func spellInt(v *int) string {
	if v == nil {
		return absent
	}
	return strconv.Itoa(*v)
}

func spellString(v *string) string {
	if v == nil {
		return absent
	}
	return *v
}

func spellBool(v *bool) string {
	if v == nil {
		return absent
	}
	return strconv.FormatBool(*v)
}
