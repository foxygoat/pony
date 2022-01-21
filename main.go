package main

import (
	"os"

	"foxygo.at/jig/bones"
	"foxygo.at/jig/serve"
	"github.com/alecthomas/kong"
)

var version = "v0.0.0"

type config struct {
	Version kong.VersionFlag `short:"V" help:"Print version information" group:"Other"`
	Serve   cmdServe         `cmd:"" help:"Serve GRPC services"`
	Bones   cmdBones         `cmd:"" help:"Generate skeleton jsonnet methods"`
}

type cmdServe struct {
	ProtoSet []string       `short:"p" help:"Protoset .pb files containing service and deps"`
	LogLevel serve.LogLevel `help:"Server logging level." default:"error"`
	Listen   string         `short:"l" default:"localhost:8080" help:"TCP listen address"`

	Dirs []string `arg:"" help:"Directory containing method definitions and protoset .pb file"`
}

type cmdBones struct {
	ProtoSet  string   `short:"p" help:"Protoset .pb file containing service and deps" required:""`
	MethodDir string   `short:"m" help:"Directory to write method definitions to"`
	Force     bool     `short:"f" help:"Overwrite existing bones files"`
	Targets   []string `arg:"" optional:"" help:"Target pkg/service/method to generate"`
}

func main() {
	cli := &config{}
	kctx := kong.Parse(cli, kong.Vars{"version": version})
	err := kctx.Run()
	kctx.FatalIfErrorf(err)
}

func (cs *cmdServe) Run() error {
	withLogger := serve.WithLogger(serve.NewLogger(os.Stderr, cs.LogLevel))
	withDirs := serve.WithDirs(cs.Dirs...)
	s, err := serve.NewServer(withDirs, withLogger)
	if err != nil {
		return err
	}
	return s.ListenAndServe(cs.Listen)
}

func (cb *cmdBones) Run() error {
	return bones.Generate(cb.ProtoSet, cb.MethodDir, cb.Force, cb.Targets)
}
