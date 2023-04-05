package build

import (
	"context"
	"testing"

	bkclient "github.com/moby/buildkit/client"
)

func TestBuildKernel(t *testing.T) {
	ctr := KernelBuildBase()
	source, err := GetKernelSource("6.2.2")
	if err != nil {
		t.Fatal(err)
	}

	_, kern, _ := BuildKernel(ctr, source, nil)
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

	ctx := context.Background()

	def, err := kern.State().Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Solve(ctx, def, bkclient.SolveOpt{}, ch)
	if err != nil {
		t.Fatal(err)
	}
	<-done
}
