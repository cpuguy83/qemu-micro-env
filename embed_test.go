package main

import (
	"context"
	"testing"

	bkclient "github.com/moby/buildkit/client"
)

func TestBuildMod(t *testing.T) {
	ctx := context.Background()

	for name, builder := range Modules() {
		name := name
		builder := builder

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f, err := builder()
			if err != nil {
				t.Fatal(err)
			}

			def, err := f.Marshal(ctx)
			if err != nil {
				t.Fatal(err)
			}

			ch := make(chan *bkclient.SolveStatus)
			done := make(chan struct{})
			go func() {
				for s := range ch {
					for _, v := range s.Logs {
						t.Log("\n" + string(v.Data))
					}
				}
				close(done)
			}()

			_, err = client.Solve(ctx, def, bkclient.SolveOpt{}, ch)
			if err != nil {
				t.Fatal(err)
			}
			<-done
		})
	}
}
