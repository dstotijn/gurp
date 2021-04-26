package main

import (
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/gorilla/mux"
	"github.com/mitchellh/go-homedir"

	"github.com/dstotijn/hetty/pkg/api"
	"github.com/dstotijn/hetty/pkg/db/sqlite"
	"github.com/dstotijn/hetty/pkg/proj"
	"github.com/dstotijn/hetty/pkg/proxy"
	"github.com/dstotijn/hetty/pkg/reqlog"
	"github.com/dstotijn/hetty/pkg/scope"
)

var (
	caCertFile string
	caKeyFile  string
	projPath   string
	projName   string
	addr       string
)

//go:embed admin
//go:embed admin/_next/static
//go:embed admin/_next/static/chunks/pages/*.js
//go:embed admin/_next/static/*/*.js
var adminContent embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatalf("[ERROR]: %v", err)
	}
}

func run() error {
	flag.StringVar(&caCertFile, "cert", "~/.hetty/hetty_cert.pem",
		"CA certificate filepath. Creates a new CA certificate if file doesn't exist")
	flag.StringVar(&caKeyFile, "key", "~/.hetty/hetty_key.pem",
		"CA private key filepath. Creates a new CA private key if file doesn't exist")
	flag.StringVar(&projPath, "projects", "~/.hetty/projects", "Projects directory path")
	flag.StringVar(&projName, "project", "", "Project to open on start")
	flag.StringVar(&addr, "addr", ":8080", "TCP address to listen on, in the form \"host:port\"")
	flag.Parse()

	// Expand `~` in filepaths.
	caCertFile, err := homedir.Expand(caCertFile)
	if err != nil {
		return fmt.Errorf("could not parse CA certificate filepath: %w", err)
	}

	caKeyFile, err := homedir.Expand(caKeyFile)
	if err != nil {
		return fmt.Errorf("could not parse CA private key filepath: %w", err)
	}

	projPath, err := homedir.Expand(projPath)
	if err != nil {
		return fmt.Errorf("could not parse projects filepath: %w", err)
	}

	// Load existing CA certificate and key from disk, or generate and write
	// to disk if no files exist yet.
	caCert, caKey, err := proxy.LoadOrCreateCA(caKeyFile, caCertFile)
	if err != nil {
		return fmt.Errorf("could not create/load CA key pair: %w", err)
	}

	db, err := sqlite.New(projPath)
	if err != nil {
		return fmt.Errorf("could not initialize database client: %w", err)
	}

	projService, err := proj.NewService(db)
	if err != nil {
		return fmt.Errorf("could not create new project service: %w", err)
	}
	defer projService.Close()

	if projName != "" {
		_, err = projService.Open(context.Background(), projName)
		if err != nil {
			log.Fatalf("[FATAL] Could not open project: %v", err)
		}
	}

	scope := scope.New(db, projService)

	reqLogService := reqlog.NewService(reqlog.Config{
		Scope:          scope,
		ProjectService: projService,
		Repository:     db,
	})

	p, err := proxy.NewProxy(caCert, caKey)
	if err != nil {
		return fmt.Errorf("could not create proxy: %w", err)
	}

	p.UseRequestModifier(reqLogService.RequestModifier)
	p.UseResponseModifier(reqLogService.ResponseModifier)

	fsSub, err := fs.Sub(adminContent, "admin")
	if err != nil {
		return fmt.Errorf("could not prepare subtree file system: %w", err)
	}

	adminHandler := http.FileServer(http.FS(fsSub))
	router := mux.NewRouter().SkipClean(true)
	adminRouter := router.MatcherFunc(func(req *http.Request, match *mux.RouteMatch) bool {
		hostname, _ := os.Hostname()
		host, _, _ := net.SplitHostPort(req.Host)
		return strings.EqualFold(host, hostname) || (req.Host == "hetty.proxy" || req.Host == "localhost:8080")
	}).Subrouter().StrictSlash(true)

	// GraphQL server.
	adminRouter.Path("/api/playground/").Handler(playground.Handler("GraphQL Playground", "/api/graphql/"))
	adminRouter.Path("/api/graphql/").Handler(
		handler.NewDefaultServer(api.NewExecutableSchema(api.Config{Resolvers: &api.Resolver{
			RequestLogService: reqLogService,
			ProjectService:    projService,
			ScopeService:      scope,
		}})))

	// Admin interface.
	adminRouter.PathPrefix("").Handler(adminHandler)

	// Fallback (default) is the Proxy handler.
	router.PathPrefix("").Handler(p)

	s := &http.Server{
		Addr:         addr,
		Handler:      router,
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}, // Disable HTTP/2
	}

	log.Printf("[INFO] Running server on %v ...", addr)

	err = s.ListenAndServe()
	if err != nil && errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server closed unexpected: %w", err)
	}

	return nil
}
