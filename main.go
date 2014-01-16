// Copyright 2013 Frederik Zipp.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Pythia is a web application front-end for the Go source code oracle.
package main

import (
	"flag"
	"fmt"
	"go/build"
	"go/token"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"

	"code.google.com/p/go.tools/importer"
	"code.google.com/p/go.tools/oracle"
)

var (
	httpAddr = flag.String("http", ":8080", "HTTP listen address")
	verbose  = flag.Bool("v", false, "Verbose mode: print incoming queries")
	open     = flag.Bool("open", true, "Try to open browser")
	tags     = flag.String("tags", "", "Tags to use when importing packages")
	args     []string
	files    []string
	packages []*importer.PackageInfo
	iprog    *importer.Program
	ora      *oracle.Oracle
	mutex    sync.Mutex
)

func init() {
	if os.Getenv("GOMAXPROCS") == "" {
		n := runtime.NumCPU()
		if n < 4 {
			n = 4
		}
		runtime.GOMAXPROCS(n)
	}
}

const useHelp = "Run 'pythia -help' for more information.\n"

const helpMessage = `Web frontend for the Go source code oracle.
Usage: pythia [<flag> ...] <args> ...

The -http flag specifies the HTTP service address (e.g., ':6060').

The -tags flag specifies comma separated tags to use when importing
code (e.g., 'foo,!darwin').

The -open flag determines, whether the application should try to
open the browser. It is set to 'true' by default. If set to 'false'
the browser will not be launched.

The -v flag enables verbose mode, in which every incoming query
to the oracle is logged to the standard output.

Examples:

Start pythia with the scope of package oracle:
% pythia code.google.com/p/go.tools/cmd/oracle

Start pythia with the scope of package image/png on port 8081,
but don't open the browser:
% pythia -http=:8081 -open=false image/png
` + importer.FromArgsUsage

func main() {
	flag.Usage = func() { fmt.Fprint(os.Stderr, useHelp) }
	flag.CommandLine.Init(os.Args[0], flag.ContinueOnError)
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			fmt.Println(helpMessage)
			fmt.Println("Flags:")
			flag.PrintDefaults()
		}
		os.Exit(2)
	}
	args = flag.Args()
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "Error: no package arguments.\n"+useHelp)
		os.Exit(2)
	}

	var err error
	settings := build.Default
	settings.BuildTags = strings.Split(*tags, ",")
	conf := importer.Config{
		Build:         &settings,
		SourceImports: true,
	}
	_, err = conf.FromArgs(args)
	if err != nil {
		exitError(err)
	}
	iprog, err = conf.Load()
	if err != nil {
		exitError(err)
	}
	ora, err = oracle.New(iprog, nil, false)
	if err != nil {
		exitError(err)
	}
	files = scopeFiles(iprog)
	packages = sortedPackages(iprog)

	registerHandlers()

	srv := &http.Server{Addr: *httpAddr}
	l, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		exitError(err)
	}
	if *open {
		url := fmt.Sprintf("http://localhost%s/", *httpAddr)
		if !startBrowser(url) {
			fmt.Println(url)
		}
	}
	exitError(srv.Serve(l))
}

func registerHandlers() {
	http.HandleFunc("/", serveIndex)
	http.HandleFunc("/source", serveSource)
	http.HandleFunc("/file", serveFile)
	http.HandleFunc("/query", serveQuery)
	staticPrefix := "/static/"
	http.Handle(staticPrefix, http.StripPrefix(staticPrefix, http.HandlerFunc(serveStatic)))
}

// byPath makes a slice of package infos sortable by package path.
type byPath []*importer.PackageInfo

func (p byPath) Len() int           { return len(p) }
func (p byPath) Less(i, j int) bool { return p[i].Pkg.Path() < p[j].Pkg.Path() }
func (p byPath) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// sortedPackages returns all packages of a program, sorted by package path.
func sortedPackages(iprog *importer.Program) []*importer.PackageInfo {
	pkgs := make([]*importer.PackageInfo, 0, len(iprog.AllPackages))
	for _, p := range iprog.AllPackages {
		pkgs = append(pkgs, p)
	}
	sort.Sort(byPath(pkgs))
	return pkgs
}

// scopeFiles returns a new slice containing the full paths of all the files
// imported by an importer, sorted in increasing order.
func scopeFiles(iprog *importer.Program) []string {
	files := make([]string, 0)
	iprog.Fset.Iterate(func(f *token.File) bool {
		files = append(files, f.Name())
		return true
	})
	sort.Strings(files)
	return files
}

// startBrowser tries to open the URL in a browser
// and reports whether it succeeds.
func startBrowser(url string) bool {
	// try to start the browser
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"open"}
	case "windows":
		args = []string{"cmd", "/c", "start"}
	default:
		args = []string{"xdg-open"}
	}
	cmd := exec.Command(args[0], append(args[1:], url)...)
	return cmd.Start() == nil
}

func exitError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// cmdLine returns what the command line would look like if the oracle was
// invoked via command line with the given arguments.
func cmdLine(mode, pos, format string, scope []string) string {
	return fmt.Sprintf("oracle -pos=%s -format=%s %s %s",
		pos, format, mode, strings.Join(scope, " "))
}
