module github.com/cpuguy83/qemu-micro-env/cmd/runner

go 1.19

require (
	dagger.io/dagger v0.4.3
	github.com/cpuguy83/go-docker v0.0.0-20221205194633-b6caf18b7d0a
	github.com/cpuguy83/go-mod-copies/platforms v0.1.0
	github.com/cpuguy83/pipes v0.0.0-20210822175459-cdd9171bf6ca
	golang.org/x/crypto v0.0.0-20210921155107-089bfa567519
	golang.org/x/exp v0.0.0-20230113213754-f9f960f08ad4
	golang.org/x/sys v0.4.0
)

replace github.com/cpuguy83/qemu-micro-env/cmd/init => ../init

require (
	github.com/Khan/genqlient v0.5.0 // indirect
	github.com/Microsoft/go-winio v0.4.15 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/iancoleman/strcase v0.2.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.3-0.20220303224323-02efb9a75ee1 // indirect
	github.com/vektah/gqlparser/v2 v2.5.1 // indirect
	golang.org/x/sync v0.1.0 // indirect
)
