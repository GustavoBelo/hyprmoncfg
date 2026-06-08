package profileio

import (
	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/profile"
	"github.com/crmne/hyprmoncfg/internal/render"
)

func SaveWithSidecars(store *profile.Store, p profile.Profile) error {
	sidecars, err := renderSidecars(p)
	if err != nil {
		return err
	}
	if err := store.Save(p); err != nil {
		return err
	}
	return writeSidecars(store, p.Name, sidecars)
}

type sidecars struct {
	conf string
	lua  string
}

func renderSidecars(p profile.Profile) (sidecars, error) {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return sidecars{}, err
	}

	monitors := render.ProfileMonitors(p)

	conf, err := render.RenderConfig(p, monitors, render.Options{
		Format:       config.HyprConfigLegacy,
		UseMonitorV2: true,
	})
	if err != nil {
		return sidecars{}, err
	}

	lua, err := render.RenderConfig(p, monitors, render.Options{
		Format:       config.HyprConfigLua,
		UseMonitorV2: true,
	})
	if err != nil {
		return sidecars{}, err
	}

	return sidecars{conf: conf, lua: lua}, nil
}

func writeSidecars(store *profile.Store, name string, files sidecars) error {
	if err := store.Ensure(); err != nil {
		return err
	}
	paths := store.PathsForName(name)
	if err := config.WriteFileAtomic(paths.Conf, []byte(files.conf), 0o644); err != nil {
		return err
	}
	if err := config.WriteFileAtomic(paths.Lua, []byte(files.lua), 0o644); err != nil {
		return err
	}
	return nil
}
