// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

// SecretName is a key in the host's secret store. The raw strings are the
// contract with that store.
type SecretName string

// The three keys in the host's secret store.
const (
	SecretDeviceToken SecretName = "deviceToken"
	SecretDeviceID    SecretName = "deviceID"
	SecretUserToken   SecretName = "userToken"
)

// Secret is one stored credential.
type Secret struct {
	Name  SecretName
	Value string
}
