package cmd

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"setu/pkg/ui"
)

const serveUsage = `usage: setu serve [flags]

Serve the knowledge base web UI: browse every previous run discovered under
.ai-context workspaces (sessions, requirements, context files, git history).

Flags:
  --addr <host:port>  Listen address (default 127.0.0.1:8080)
  --dir <path>        Directory to scan for .ai-context workspaces
                      (repeatable; default: current directory)
`

type serveOptions struct {
	addr string
	dirs []string
}

func parseServeFlags(args []string) (serveOptions, error) {
	o := serveOptions{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func(flag string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing value for %s", flag)
			}
			i++
			return args[i], nil
		}
		var err error
		switch arg {
		case "--addr":
			o.addr, err = next(arg)
		case "--dir":
			var dir string
			if dir, err = next(arg); err == nil {
				o.dirs = append(o.dirs, dir)
			}
		default:
			return o, fmt.Errorf("unknown flag %q", arg)
		}
		if err != nil {
			return o, err
		}
	}
	if o.addr == "" {
		o.addr = "127.0.0.1:8080"
	}
	if len(o.dirs) == 0 {
		o.dirs = []string{"."}
	}
	return o, nil
}

// runServe implements `setu serve`: a read-only web view over the knowledge
// base written by previous runs.
func runServe(args []string, out, errOut io.Writer) int {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprint(out, serveUsage)
			return 0
		}
	}
	opts, err := parseServeFlags(args)
	if err != nil {
		fmt.Fprintf(errOut, "setu serve: %v\n\n%s", err, serveUsage)
		return 2
	}

	provider := ui.DiscoverRoots(opts.dirs)
	if k, err := provider(); err != nil {
		fmt.Fprintf(errOut, "setu serve: scan: %v\n", err)
	} else {
		fmt.Fprintf(out, "setu serve: %s\n", ui.Summary(k))
	}

	ln, err := net.Listen("tcp", opts.addr)
	if err != nil {
		fmt.Fprintf(errOut, "setu serve: listen %s: %v\n", opts.addr, err)
		return 1
	}
	fmt.Fprintf(out, "Knowledge base UI at http://%s  (scanning: %s)\nPress Ctrl+C to stop.\n",
		ln.Addr().String(), strings.Join(opts.dirs, ", "))

	srv := &http.Server{
		Handler:           ui.NewHandler(provider),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(errOut, "setu serve: %v\n", err)
		return 1
	}
	return 0
}
