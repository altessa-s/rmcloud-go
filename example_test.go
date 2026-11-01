// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	rmcloud "github.com/altessa-s/rmcloud-go"
)

func ExampleBlobHash() {
	fmt.Println(rmcloud.BlobHash([]byte("hello")))
	// Output: 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
}

func ExampleParse() {
	index, err := rmcloud.Parse("4\n0:.:2:160\naaaa:80000000:doc-1:2:120\nbbbb:0:doc-1.pdf:0:40\n")
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, entry := range index.Entries {
		fmt.Println(entry.DocumentID, entry.Size, entry.IsDocument())
	}
	fmt.Println("count mismatch:", index.CountMismatch())
	// Output:
	// doc-1 120 true
	// doc-1.pdf 40 false
	// count mismatch: false
}

func ExampleSerializeRoot() {
	fmt.Print(rmcloud.SerializeRoot([]rmcloud.Entry{
		{Hash: "bbbb", Type: rmcloud.FileType, DocumentID: "doc-2", Size: 40},
		{Hash: "aaaa", Type: rmcloud.FileType, DocumentID: "doc-1", Subfiles: 2, Size: 120},
	}))
	// Output:
	// 4
	// 0:.:2:160
	// aaaa:0:doc-1:2:120
	// bbbb:0:doc-2:0:40
}

func ExampleFromEnvironment() {
	endpoints, err := rmcloud.FromEnvironment(map[string]string{
		"RMCLOUD_SYNC_HOST": "http://user:secret@sync.example",
	})
	fmt.Println(endpoints.SyncHost.Host == rmcloud.Production().SyncHost.Host)
	fmt.Println(err)
	// Output:
	// true
	// rmcloud: ignoring RMCLOUD_SYNC_HOST for host sync.example: only HTTPS, or HTTP on the loopback
}

// The store is the caller's own; any SecretStore works.
func ExampleNew() {
	var store rmcloud.SecretStore // a credential store, a file, or a vault
	client, err := rmcloud.New(store, rmcloud.WithEndpoints(rmcloud.Production()),
		rmcloud.WithLogger(slog.Default()),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.Close()
}

func ExampleClient_MissingFiles() {
	ctx := context.Background()
	var client *rmcloud.Client // from rmcloud.New
	missing, err := client.MissingFiles(ctx, []string{rmcloud.BlobHash([]byte("page"))})
	switch {
	case errors.Is(err, rmcloud.ErrNotRegistered):
		fmt.Println("register the device first")
	case err != nil:
		fmt.Println(err)
	default:
		fmt.Println(len(missing), "blobs to upload")
	}
}
