package main

import (
	"github.com/urfave/cli/v2"
	"sirherobrine23.com.br/Sirherobrine23/drivefs"
)

var (
	GdriveFS = &drivefs.GdriveFS{}
)

func main() {
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Usage:   "Google Drive credentials and token file",
			Aliases: []string{"g"},
			EnvVars: []string{"CONFIG_PATH"},
			DefaultText: "config.json",
		},
		&cli.UintFlag{
			Name: "port",
			Usage: "Port to listen on",
			Aliases: []string{"p"},
			EnvVars: []string{"PORT"},
			Value: 8080,
		},
	}
}
