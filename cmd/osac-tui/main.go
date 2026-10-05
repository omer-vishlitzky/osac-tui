package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/osac-project/osac-tui/internal/client"
	"github.com/osac-project/osac-tui/internal/ui"
	"github.com/osac-project/osac-tui/internal/version"
)

type options struct {
	address     string
	tls         bool
	insecure    bool
	caFile      string
	token       string
	osacVersion string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "osac-tui: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	var options options
	flag.StringVar(&options.address, "address", os.Getenv("OSAC_ADDRESS"), "fulfillment-service gRPC address (discovered from Kubernetes when omitted)")
	flag.BoolVar(&options.tls, "tls", false, "use TLS for the gRPC connection")
	flag.BoolVar(&options.insecure, "insecure", false, "skip TLS certificate verification (unsafe)")
	flag.StringVar(&options.caFile, "ca-file", "", "PEM file containing an additional CA certificate")
	flag.StringVar(&options.token, "token", os.Getenv("OSAC_TOKEN"), "bearer token for the gRPC connection (requested from Kubernetes when omitted)")
	flag.StringVar(&options.osacVersion, "osac-version", os.Getenv("OSAC_VERSION"), "OSAC API version to display")
	flag.Parse()

	var clusterCA []byte
	if options.address == "" || options.token == "" {
		login, err := ui.PromptLogin(options.address == "", options.token == "")
		if err != nil {
			return err
		}
		if options.address == "" {
			options.address = login.Address
			options.tls = true
		}
		if options.token == "" {
			options.token = login.Token
		}
		clusterCA = login.CAPEM
	}

	tlsConfig, err := loadTLSConfig(options, clusterCA)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	api, err := client.Dial(ctx, client.Config{
		Address:   options.address,
		TLSConfig: tlsConfig,
		Token:     options.token,
	})
	if err != nil {
		return err
	}
	defer api.Close()

	_, err = tea.NewProgram(ui.New(api, version.Value, options.osacVersion), tea.WithAltScreen()).Run()
	return err
}

func loadTLSConfig(options options, clusterCA []byte) (*tls.Config, error) {
	if !options.tls && options.insecure {
		return nil, errors.New("--insecure requires --tls")
	}
	if !options.tls && options.caFile != "" {
		return nil, errors.New("--ca-file requires --tls")
	}
	if !options.tls {
		return nil, nil
	}

	config := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: options.insecure,
	}
	if options.caFile == "" && len(clusterCA) == 0 {
		return config, nil
	}

	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if options.caFile != "" {
		data, err := os.ReadFile(options.caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %q: %w", options.caFile, err)
		}
		if ok := roots.AppendCertsFromPEM(data); !ok {
			return nil, fmt.Errorf("CA file %q contains no certificates", options.caFile)
		}
	}
	if len(clusterCA) > 0 {
		if ok := roots.AppendCertsFromPEM(clusterCA); !ok {
			return nil, errors.New("cluster CA contains no certificates")
		}
	}
	config.RootCAs = roots
	return config, nil
}
