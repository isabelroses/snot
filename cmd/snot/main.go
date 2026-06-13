package main

import (
	"github.com/alecthomas/kong"
)

var Version = "dev"

type CLI struct {
	Version kong.VersionFlag `help:"Show version and exit."`
	Config  string           `help:"Path to the TOML config file." default:"/etc/snot/config.toml" env:"SNOT_CONFIG" type:"path"`

	Serve ServeCmd `cmd:"" help:"Run the knot server."`
	Check CheckCmd `cmd:"" help:"Validate configuration: database, Forgejo user, repository root."`
	Repos ReposCmd `cmd:"" help:"List the public Forgejo repos this knot exposes, with their repo DIDs."`
}

func main() {
	var cli CLI
	kctx := kong.Parse(&cli,
		kong.Name("snot"),
		kong.Description("A tangled knot server backed by a Forgejo instance. "+
			"Configured via a TOML file (--config), with SNOT_* env vars overriding individual keys."),
		kong.UsageOnError(),
		kong.Vars{"version": Version},
	)

	kctx.FatalIfErrorf(kctx.Run(&cli))
}
