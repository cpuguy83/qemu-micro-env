module github.com/cpuguy83/qemu-micro-env/cmd/runner

go 1.19

require (
	dagger.io/dagger v0.4.3
	github.com/cpuguy83/go-mod-copies/platforms v0.1.0
	github.com/cpuguy83/qemu-micro-env/cmd/init v0.0.0
	github.com/moby/buildkit v0.11.0
	golang.org/x/exp v0.0.0-20230113213754-f9f960f08ad4
	golang.org/x/sys v0.4.0
)

replace github.com/cpuguy83/qemu-micro-env/cmd/init => ../init

require (
	github.com/Khan/genqlient v0.5.0 // indirect
	github.com/Microsoft/go-winio v0.5.2 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/containerd/containerd v1.6.14 // indirect
	github.com/containerd/continuity v0.3.0 // indirect
	github.com/docker/distribution v2.8.1+incompatible // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/iancoleman/strcase v0.2.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.3-0.20220303224323-02efb9a75ee1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.9.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.1 // indirect
	golang.org/x/crypto v0.2.0 // indirect
	golang.org/x/sync v0.1.0 // indirect
	google.golang.org/genproto v0.0.0-20220706185917-7780775163c4 // indirect
	google.golang.org/grpc v1.50.1 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)
