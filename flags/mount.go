package flags

import (
	"flag"
	"fmt"
	"strings"

	"github.com/cpuguy83/go-docker/container/containerapi/mount"
)

type MountSpec struct {
	mnt *mount.Mount
}

func NewMountSpec(mnt *mount.Mount) *MountSpec {
	return &MountSpec{mnt: mnt}
}

func (m *MountSpec) Set(s string) error {
	if s == "" {
		return nil
	}

	var mnt mount.Mount
	split := strings.Split(s, ",")
	for _, s := range split {
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("invalid mount spec: %s", s)
		}
		switch k {
		case "type":
			mnt.Type = mount.Type(v)
		case "source":
			mnt.Source = v
		default:
			return fmt.Errorf("unknown mount spec key: %s", k)
		}
	}
	if mnt.Type == "" || mnt.Source == "" {
		return fmt.Errorf("invalid mount spec, both type and source keys are required: %s", s)
	}
	m.mnt = &mnt
	return nil
}

func (m *MountSpec) String() string {
	if m.mnt == nil {
		return ""
	}
	return fmt.Sprintf("type=%s,source=%s", m.mnt.Type, m.mnt.Source)
}

func (m *MountSpec) AsMount() Optional[mount.Mount] {
	if m.mnt == nil {
		return Optional[mount.Mount]{some: false}
	}
	mm := mount.Mount(*m.mnt)
	return Optional[mount.Mount]{v: mm, some: true}
}

const (
	usage = "mount spec, provided as a comma separated list of key=value pairs. Valid keys are: type, source"
)

func AddMountSpecFlag(f *flag.FlagSet, m *MountSpec, name string) {
	f.Var(m, name, usage)
}
