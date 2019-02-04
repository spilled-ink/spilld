package imapserver_test

import (
	"context"
	"testing"
	"time"

	"crawshaw.io/iox"
	"spilled.ink/imap/imaptest"
)

func Test(t *testing.T) {
	filer := iox.NewFiler(0)
	filer.DefaultBufferMemSize = 1 << 20
	filer.Logf = t.Logf
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		filer.Shutdown(ctx)
	}()

	t.Run("Memory", func(t *testing.T) {
		for _, test := range imaptest.Tests {
			test := test
			t.Run(test.Name, func(t *testing.T) {
				t.Parallel()
				dataStore := &imaptest.MemoryStore{
					Filer: filer,
				}
				server, err := imaptest.InitTestServer(filer, dataStore, dataStore)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					dataStore.Close()
					if err := server.Shutdown(); err != nil {
						t.Fatal(err)
					}
				}()

				test.Fn(t, server)
			})
		}
	})
}
