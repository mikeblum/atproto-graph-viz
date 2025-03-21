package version

import (
	"runtime/debug"
	"strings"

	"github.com/mikeblum/atgraph.dev/conf"
)

const (
	modulePath = "github.com/mikeblum/atgraph.dev"
)

// resolve build version
func BuildVersion() (*debug.Module, bool) {
	log := conf.NewLog()
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			log.Debug("build-info", "path", dep.Path, "version", dep.Version)
			if strings.EqualFold(dep.Path, modulePath) {
				return dep, true
			}
		}
	}
	return &debug.Module{
		Path:    modulePath,
		Version: "develop",
	}, false
}
