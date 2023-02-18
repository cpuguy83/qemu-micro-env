module github.com/cpuguy83/qemu-micro-env/cmd/runner

go 1.19

require (
	dagger.io/dagger v0.4.4
	github.com/antonfisher/nested-logrus-formatter v1.3.1
	github.com/cpuguy83/go-docker v0.0.0-20230217011940-bd4c5e8a6785
	github.com/cpuguy83/go-mod-copies/platforms v0.1.0
	github.com/cpuguy83/go-vsock v0.0.0-20230125191134-0e74777801b7
	github.com/cpuguy83/pipes v0.0.0-20210822175459-cdd9171bf6ca
	github.com/sirupsen/logrus v1.9.0
	golang.org/x/crypto v0.5.0
	golang.org/x/exp v0.0.0-20230113213754-f9f960f08ad4
	golang.org/x/sync v0.1.0
	golang.org/x/sys v0.5.0
)

// replace github.com/cpuguy83/qemu-micro-env/cmd/init => ../init

require (
	github.com/Khan/genqlient v0.5.0 // indirect
	github.com/Microsoft/go-winio v0.6.0 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/iancoleman/strcase v0.2.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.0-rc2 // indirect
	github.com/vektah/gqlparser/v2 v2.5.1 // indirect
	golang.org/x/mod v0.8.0 // indirect
	golang.org/x/tools v0.6.0 // indirect
)
