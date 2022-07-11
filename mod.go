package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/jessevdk/go-flags"
	"github.com/nkcr/hodor/config"
	"github.com/nkcr/hodor/deployer"
	"github.com/nkcr/hodor/server"
	"github.com/rs/zerolog"
	"github.com/tidwall/buntdb"
)

// Version contains the current or build version. This variable can be changed
// at build time with:
//
//   go build -ldflags="-X 'main.Version=v1.0.0'"
//
// Version should be fetched from git: `git describe --tags`
var Version = "unknown"

// BuildTime indicates the time at which the binary has been built. Must be set
// as with Version.
var BuildTime = "unknown"

var logout = zerolog.ConsoleWriter{
	Out:        os.Stdout,
	TimeFormat: time.RFC3339,
}

// args defines the CLI arguments. You can always use -h to see the help.
type args struct {
	Config     string `short:"c" long:"config" default:"config.json" description:"File path of the configuration."`
	DBFilePath string `short:"d" long:"dbfilepath" default:"hodor.db" description:"File path of the database."`
	HTTPListen string `short:"l" long:"listen" default:"0.0.0.0:3333" description:"The listen address of the HTTP server that servers the API."`
	Version    bool   `short:"v" long:"version" description:"Displays the version."`
}

func main() {
	var args args
	parser := flags.NewParser(&args, flags.Default)

	remaining, err := parser.Parse()
	if err != nil {
		flagsErr, ok := err.(*flags.Error)
		if ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}

		fmt.Println("failed to parse arguments:", err.Error())
		os.Exit(1)
	}

	if len(remaining) != 0 {
		fmt.Printf("unknown flags: %v\n", remaining)
		os.Exit(1)
	}

	if args.Version {
		fmt.Println("Hodor", Version, "-", BuildTime)
		os.Exit(0)
	}

	var logger = zerolog.New(logout).Level(zerolog.InfoLevel).
		With().Timestamp().Logger().
		With().Caller().Logger()

	logger.Info().Msgf("hi,\n"+
		"┌───────────────────────────────────────────────┐\n"+
		"│    ** Hookable Deployment of Releases **\t│\n"+
		"├───────────────────────────────────────────────┤\n"+
		"│ Version %s │ Build time %s\t│\n"+
		"├───────────────────────────────────────────────┤\n"+
		"│ Config %s\t│\n"+
		"├───────────────────────────────────────────────┤\n"+
		"│ DBFilePath %s\t│\n"+
		"├───────────────────────────────────────────────┤\n"+
		"│ HTTPListen %s\t│\n"+
		"└───────────────────────────────────────────────┘\n",
		Version, BuildTime, args.Config, args.DBFilePath, args.HTTPListen)

	var conf config.Config

	err = conf.LoadFromJSON(args.Config)
	if err != nil {
		logger.Panic().Msgf("failed to load config: %v", err)
	}

	err = os.MkdirAll(filepath.Dir(args.DBFilePath), 0744)
	if err != nil {
		panic(fmt.Sprintf("failed to create db dir: %v", err))
	}

	db, err := buntdb.Open(args.DBFilePath)
	if err != nil {
		panic(err)
	}

	defer db.Close()

	deployer := deployer.NewFileDeployer(db, conf, http.DefaultClient, logger)
	server := server.NewHookHTTP(args.HTTPListen, deployer, logger)

	wait := sync.WaitGroup{}

	wait.Add(1)
	go func() {
		defer wait.Done()
		server.Start()
		logger.Info().Msg("http server done")
	}()

	wait.Add(1)
	go func() {
		defer wait.Done()
		deployer.Start()
		logger.Info().Msg("deployer done")
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)

	<-quit

	server.Stop()
	deployer.Stop()

	wait.Wait()

	logger.Info().Msg("done")
}
