package app

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const defaultDashboardListen = "127.0.0.1:0"

//go:embed dashboard/dist
var dashboardAssets embed.FS

type dashboardOptions struct {
	Listen    string
	NoBrowser bool
}

type dashboardHTTPServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
}

type dashboardDependencies struct {
	listen        func(network, address string) (net.Listener, error)
	openURL       func(string) error
	newHTTPServer func(handler http.Handler) dashboardHTTPServer
	stdout        io.Writer
}

func defaultDashboardDependencies() dashboardDependencies {
	return dashboardDependencies{
		listen: net.Listen,
		openURL: func(rawURL string) error {
			return exec.Command("open", rawURL).Start()
		},
		newHTTPServer: func(handler http.Handler) dashboardHTTPServer {
			return &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
				IdleTimeout:       60 * time.Second,
			}
		},
		stdout: os.Stdout,
	}
}

func webCommand(args []string, version string) error {
	opts, err := parseDashboardOptions(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveDashboard(ctx, opts, newAppService(), version, defaultDashboardDependencies())
}

func parseDashboardOptions(args []string) (dashboardOptions, error) {
	opts := dashboardOptions{}
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.Listen, "listen", defaultDashboardListen, "loopback address to listen on")
	flags.BoolVar(&opts.NoBrowser, "no-browser", false, "do not open the dashboard in a browser")
	if err := flags.Parse(args); err != nil {
		return dashboardOptions{}, err
	}
	if flags.NArg() != 0 {
		return dashboardOptions{}, fmt.Errorf("web does not accept positional arguments")
	}
	return opts, nil
}

func serveDashboard(ctx context.Context, opts dashboardOptions, service dashboardService, version string, deps dashboardDependencies) error {
	if err := validateDashboardListen(opts.Listen); err != nil {
		return err
	}
	if deps.listen == nil || deps.newHTTPServer == nil {
		return fmt.Errorf("dashboard server dependencies are incomplete")
	}
	if deps.openURL == nil {
		deps.openURL = func(string) error { return nil }
	}
	if deps.stdout == nil {
		deps.stdout = io.Discard
	}

	network := "tcp4"
	host, _, _ := net.SplitHostPort(opts.Listen)
	if address, err := netip.ParseAddr(host); err == nil && address.Is6() {
		network = "tcp6"
	}
	listener, err := deps.listen(network, opts.Listen)
	if err != nil {
		return fmt.Errorf("listen for dashboard: %w", err)
	}
	defer listener.Close()

	authority := listener.Addr().String()
	rawURL := "http://" + authority + "/"
	csrfToken, err := newDashboardCSRFToken()
	if err != nil {
		return err
	}
	handler, err := newDashboardHandler(service, version, authority, rawURL, csrfToken)
	if err != nil {
		return err
	}
	server := deps.newHTTPServer(handler)

	if _, err := fmt.Fprintln(deps.stdout, rawURL); err != nil {
		return fmt.Errorf("print dashboard URL: %w", err)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	if !opts.NoBrowser {
		if err := deps.openURL(rawURL); err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
			<-serveErr
			return fmt.Errorf("open dashboard: %w", err)
		}
	}

	select {
	case err := <-serveErr:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve dashboard: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down dashboard: %w", err)
		}
		err := <-serveErr
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve dashboard: %w", err)
		}
		return nil
	}
}

func validateDashboardListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid dashboard listen address %q: %w", address, err)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("invalid dashboard listen address %q: port is required", address)
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil || !parsed.IsLoopback() {
		return fmt.Errorf("dashboard listen address must use a loopback IP")
	}
	return nil
}

func dashboardSPAHandler() (http.Handler, error) {
	dist, err := fs.Sub(dashboardAssets, "dashboard/dist")
	if err != nil {
		return nil, fmt.Errorf("load embedded dashboard assets: %w", err)
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" && path != "index.html" {
			if info, err := fs.Stat(dist, path); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}

		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			writeDashboardError(w, http.StatusInternalServerError, "internal_error", "dashboard is unavailable")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(index)
	}), nil
}
