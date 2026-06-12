package main

import (
	"github.com/alecthomas/kong"
)

var Version = "dev"

type CLI struct {
	Version kong.VersionFlag `help:"Show version and exit."`

	Serve ServeCmd `cmd:"" help:"Run the knot server."`
	Check CheckCmd `cmd:"" help:"Validate configuration: database, Forgejo user, repository root."`
	Repos ReposCmd `cmd:"" help:"List the public Forgejo repos this knot exposes, with their repo DIDs."`
}

func main() {
	var cli CLI
	kctx := kong.Parse(&cli,
		kong.Name("snot"),
		kong.Description("A tangled knot server backed by a Forgejo instance. Configuration via SNOT_* environment variables."),
		kong.UsageOnError(),
		kong.Vars{"version": Version},
	)

	kctx.FatalIfErrorf(kctx.Run())
}
